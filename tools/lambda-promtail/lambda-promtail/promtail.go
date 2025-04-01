package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/gogo/protobuf/proto"
	"github.com/golang/snappy"
	"github.com/grafana/dskit/backoff"
	"github.com/prometheus/common/model"

	"github.com/grafana/loki/pkg/logproto"
)

const (
	timeout    = 5 * time.Second
	minBackoff = 100 * time.Millisecond
	maxBackoff = 30 * time.Second
	maxRetries = 10

	reservedLabelTenantID = "__tenant_id__"

	userAgent = "lambda-promtail"
)

type entry struct {
	labels model.LabelSet
	entry  logproto.Entry
}

type batch struct {
	streams map[string]*logproto.Stream
	size    int
	client  Client
	logger  *log.Logger
}

type batchIf interface {
	add(ctx context.Context, e entry) error
	encode() ([]byte, int, error)
	createPushRequest() (*logproto.PushRequest, int)
	flushBatch(ctx context.Context) error
}

func newBatch(
	ctx context.Context,
	pClient Client,
	logger *log.Logger,
	entries ...entry,
) (*batch, error) {
	b := &batch{
		streams: map[string]*logproto.Stream{},
		client:  pClient,
		logger:  logger,
	}

	for _, entry := range entries {
		if err := b.add(ctx, entry); err != nil {
			return nil, err
		}
	}

	return b, nil
}

func (b *batch) add(ctx context.Context, e entry) error {
	level.Debug(*b.logger).Log("msg", "Adding entry to batch")

	labels := labelsMapToString(e.labels, reservedLabelTenantID)

	stream, ok := b.streams[labels]
	if !ok {
		b.streams[labels] = &logproto.Stream{
			Labels:  labels,
			Entries: []logproto.Entry{},
		}
		stream = b.streams[labels]
	}

	stream.Entries = append(stream.Entries, e.entry)
	b.size += len(e.entry.Line)

	if b.size > batchSize {
		return b.flushBatch(ctx)
	}

	return nil
}

func labelsMapToString(ls model.LabelSet, without ...model.LabelName) string {
	lstrs := make([]string, 0, len(ls))
Outer:
	for l, v := range ls {
		for _, w := range without {
			if l == w {
				continue Outer
			}
		}
		lstrs = append(lstrs, fmt.Sprintf("%s=%q", l, v))
	}

	sort.Strings(lstrs)
	return fmt.Sprintf("{%s}", strings.Join(lstrs, ", "))
}

func (b *batch) encode() ([]byte, int, error) {
	req, entriesCount := b.createPushRequest()
	buf, err := proto.Marshal(req)
	if err != nil {
		return nil, 0, err
	}

	buf = snappy.Encode(nil, buf)
	return buf, entriesCount, nil
}

func (b *batch) createPushRequest() (*logproto.PushRequest, int) {
	req := logproto.PushRequest{
		Streams: make([]logproto.Stream, 0, len(b.streams)),
	}

	entriesCount := 0
	for _, stream := range b.streams {
		req.Streams = append(req.Streams, *stream)
		entriesCount += len(stream.Entries)
	}
	return &req, entriesCount
}

func (b *batch) flushBatch(ctx context.Context) error {
	level.Debug(*b.logger).Log("msg", "Flushing batch")

	if b.client != nil {
		level.Debug(*b.logger).Log("msg", "client is not nil, sending batch to Promtail")
		err := b.client.sendToPromtail(ctx, b)
		if err != nil {
			return err
		}
	}
	b.resetBatch()

	return nil
}

func (b *batch) resetBatch() {
	level.Debug(*b.logger).Log("msg", "Resetting batch", "size", b.size)
	b.streams = make(map[string]*logproto.Stream)
	b.size = 0
}

func (c *promtailClient) sendToPromtail(ctx context.Context, b *batch) error {
	level.Debug(*c.log).Log("msg", "Sending batch to Promtail")
	buf, _, err := b.encode()
	if err != nil {
		return err
	}

	backoff := backoff.New(ctx, *c.config.backoff)
	var status int
	for {
		// send uses `timeout` internally, so `context.Background` is good enough.
		status, err = c.send(context.Background(), buf)

		// Only retry 429s, 500s and connection-level errors.
		if status > 0 && status != 429 && status/100 != 5 {
			break
		}
		level.Error(*c.log).Log("err", fmt.Errorf("error sending batch, will retry, status: %d error: %s\n", status, err))
		backoff.Wait()

		// Make sure it sends at least once before checking for retry.
		if !backoff.Ongoing() {
			break
		}
	}

	if err != nil {
		level.Error(*c.log).Log("err", fmt.Errorf("Failed to send logs! %s\n", err))
		return err
	}

	return nil
}

func (c *promtailClient) printRequest(req *http.Request) {
	var body string
	if req.Body != nil {
		buf := new(bytes.Buffer)
		_, err := buf.ReadFrom(req.Body)
		if err != nil {
			level.Debug(*c.log).Log("Error reading request body: %v\n", err)
		}
		body = buf.String()
		req.Body = io.NopCloser(bytes.NewBuffer(buf.Bytes())) // Reset the body for the actual request
	}

	level.Debug(*c.log).Log(
		"msg", fmt.Sprintf(
			"Request: Method: %s, URL: %s, Proto: %s, Header: %v, ContentLength: %d, TransferEncoding: %v, Host: %s, Form: %v, PostForm: %v, MultipartForm: %v, Trailer: %v, RemoteAddr: %s, RequestURI: %s, TLS: %v, Body: %s",
			req.Method, req.URL.String(), req.Proto, req.Header, req.ContentLength, req.TransferEncoding, req.Host, req.Form, req.PostForm, req.MultipartForm, req.Trailer, req.RemoteAddr, req.RequestURI, req.TLS, body),
		"err", nil)
}

func (c *promtailClient) printResponse(resp *http.Response, err error) {
	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		level.Debug(*c.log).Log("Error reading body: %v\n", readErr)
		return
	}

	level.Debug(*c.log).Log("msg", fmt.Sprintf("Response: Status: %s, StatusCode: %d, Headers: %v, Body: %s",
		resp.Status, resp.StatusCode, resp.Header, string(body)),
		"err", err)
}

func (c *promtailClient) send(ctx context.Context, buf []byte) (int, error) {
	level.Debug(*c.log).Log("msg", "Sending logs to Promtail")

	ctx, cancel := context.WithTimeout(ctx, c.config.http.timeout)
	defer cancel()

	req, err := http.NewRequest("POST", writeAddress.String(), bytes.NewReader(buf))
	if err != nil {
		return -1, err
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("User-Agent", userAgent)

	if tenantID != "" {
		req.Header.Set("X-Scope-OrgID", tenantID)
	}

	if username != "" && password != "" {
		req.SetBasicAuth(username, password)
	}

	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}

	c.printRequest(req)

	resp, err := c.http.Do(req.WithContext(ctx))
	if err != nil {
		return -1, err
	}

	c.printResponse(resp, err)

	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		scanner := bufio.NewScanner(io.LimitReader(resp.Body, maxErrMsgLen))
		line := ""
		if scanner.Scan() {
			line = scanner.Text()
		}
		err = fmt.Errorf("server returned HTTP status %s (%d): %s", resp.Status, resp.StatusCode, line)
	}

	return resp.StatusCode, err
}
