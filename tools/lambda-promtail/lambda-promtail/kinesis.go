package main

import (
	"context"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/go-kit/log"
	"github.com/grafana/loki/pkg/logproto"
	"github.com/prometheus/common/model"
)

func parseKinesisEvent(ctx context.Context, b batchIf, ev *events.KinesisEvent) error {
	if ev == nil {
		return nil
	}

	for _, record := range ev.Records {
		timestamp := time.Unix(record.Kinesis.ApproximateArrivalTimestamp.Unix(), 0)

		labels := model.LabelSet{
			model.LabelName("__aws_log_type"):                 model.LabelValue("kinesis"),
			model.LabelName("__aws_kinesis_event_source_arn"): model.LabelValue(record.EventSourceArn),
		}

		labels = applyExtraLabels(labels)

		b.add(ctx, entry{labels, logproto.Entry{
			Line:      string(record.Kinesis.Data),
			Timestamp: timestamp,
		}})
	}

	return nil
}

func processKinesisEvent(ctx context.Context, ev *events.KinesisEvent, pClient Client, log *log.Logger, streamDesiredRate float64, streamRateTrackerWindowSize time.Duration) error {
	batch, _ := newBatch(ctx, pClient, streamDesiredRate, streamRateTrackerWindowSize, log)

	err := parseKinesisEvent(ctx, batch, ev)
	if err != nil {
		return err
	}

	err = pClient.sendToPromtail(ctx, batch)
	if err != nil {
		return err
	}
	return nil
}
