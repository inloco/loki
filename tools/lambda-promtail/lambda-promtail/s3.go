package main

import (
	"bufio"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"regexp"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/grafana/loki/pkg/logproto"
	"github.com/prometheus/common/model"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

var (
	// AWS Application Load Balancers
	// source:  https://docs.aws.amazon.com/elasticloadbalancing/latest/application/load-balancer-access-logs.html#access-log-file-format
	// format:  bucket[/prefix]/AWSLogs/aws-account-id/elasticloadbalancing/region/yyyy/mm/dd/aws-account-id_elasticloadbalancing_region_app.load-balancer-id_end-time_ip-address_random-string.log.gz
	// example: my-bucket/AWSLogs/123456789012/elasticloadbalancing/us-east-1/2022/01/24/123456789012_elasticloadbalancing_us-east-1_app.my-loadbalancer.b13ea9d19f16d015_20220124T0000Z_0.0.0.0_2et2e1mx.log.gz
	// VPC Flow Logs
	// source: https://docs.aws.amazon.com/vpc/latest/userguide/flow-logs-s3.html#flow-logs-s3-path
	// format: bucket-and-optional-prefix/AWSLogs/account_id/vpcflowlogs/region/year/month/day/aws_account_id_vpcflowlogs_region_flow_log_id_YYYYMMDDTHHmmZ_hash.log.gz
	// example: 123456789012_vpcflowlogs_us-east-1_fl-1234abcd_20180620T1620Z_fe123456.log.gz
	filenameRegex = regexp.MustCompile(`AWSLogs\/(?P<account_id>\d+)\/(?P<type>\w+)\/(?P<region>[\w-]+)\/(?P<year>\d+)\/(?P<month>\d+)\/(?P<day>\d+)\/\d+\_(?:elasticloadbalancing|vpcflowlogs)\_\w+-\w+-\d_(?:(?:app|nlb|net)\.*?)?(?P<src>[a-zA-Z0-9\-]+)`)

	// regex that extracts the timestamp (RFC3339) from message log
	timestampRegex = regexp.MustCompile(`\w+ (?P<timestamp>\d+-\d+-\d+T\d+:\d+:\d+\.\d+Z)`)
)

const (
	FLOW_LOG_TYPE string = "vpcflowlogs"
	LB_LOG_TYPE   string = "elasticloadbalancing"
)

func getS3Client(ctx context.Context, region string) (*s3.Client, error) {
	var s3Client *s3.Client

	if c, ok := s3Clients[region]; ok {
		s3Client = c
	} else {
		cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
		if err != nil {
			return nil, err
		}
		s3Client = s3.NewFromConfig(cfg)
		s3Clients[region] = s3Client
	}
	return s3Client, nil
}

func getELBClient(ctx context.Context, region string) (*elasticloadbalancingv2.Client, error) {
	var elbClient *elasticloadbalancingv2.Client

	if c, ok := elbClients[region]; ok {
		elbClient = c
	} else {
		cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
		if err != nil {
			return nil, err
		}
		elbClient = elasticloadbalancingv2.NewFromConfig(cfg)
		elbClients[region] = elbClient
	}
	return elbClient, nil
}

func parseS3Log(ctx context.Context, b *batch, labels map[string]string, elbTagsLabelSet model.LabelSet, obj io.ReadCloser) error {
	gzreader, err := gzip.NewReader(obj)
	if err != nil {
		return err
	}

	scanner := bufio.NewScanner(gzreader)

	skipHeader := false
	logType := labels["type"]
	if labels["type"] == FLOW_LOG_TYPE {
		skipHeader = true
		logType = "s3_vpc_flow"
	} else if labels["type"] == LB_LOG_TYPE {
		logType = "s3_lb"
	}

	ls := model.LabelSet{
		model.LabelName("__aws_log_type"):                       model.LabelValue(logType),
		model.LabelName(fmt.Sprintf("__aws_%s", logType)):       model.LabelValue(labels["src"]),
		model.LabelName(fmt.Sprintf("__aws_%s_owner", logType)): model.LabelValue(labels["account_id"]),
	}.Merge(elbTagsLabelSet)

	ls = applyExtraLabels(ls)

	timestamp := time.Now()
	var lineCount int
	for scanner.Scan() {
		log_line := scanner.Text()
		lineCount++
		if lineCount == 1 && skipHeader {
			continue
		}
		if printLogLine {
			fmt.Println(log_line)
		}

		match := timestampRegex.FindStringSubmatch(log_line)
		if len(match) > 0 {
			timestamp, err = time.Parse(time.RFC3339, match[1])
			if err != nil {
				return err
			}
		}

		if err := b.add(ctx, entry{ls, logproto.Entry{
			Line:      log_line,
			Timestamp: timestamp,
		}}); err != nil {
			return err
		}
	}

	return nil
}

func getLabels(record events.S3EventRecord) (map[string]string, error) {

	labels := make(map[string]string)

	labels["key"] = record.S3.Object.Key
	labels["bucket"] = record.S3.Bucket.Name
	labels["bucket_owner"] = record.S3.Bucket.OwnerIdentity.PrincipalID
	labels["bucket_region"] = record.AWSRegion

	match := filenameRegex.FindStringSubmatch(labels["key"])
	for i, name := range filenameRegex.SubexpNames() {
		if i != 0 && name != "" {
			labels[name] = match[i]
		}
	}

	return labels, nil
}

func getELBTags(ctx context.Context, labels map[string]string) (map[string]string, error) {
	elbClient, err := getELBClient(ctx, labels["region"])
	if err != nil {
		return nil, err
	}

	elbName := labels["src"]
	describeLoadBalancersOutput, err := elbClient.DescribeLoadBalancers(ctx, &elasticloadbalancingv2.DescribeLoadBalancersInput{
		Names: []string{elbName},
	})
	if err != nil {
		return nil, err
	}
	if len(describeLoadBalancersOutput.LoadBalancers) == 0 {
		return nil, fmt.Errorf("Failed to get ELB %s on account %s", elbName, labels["account_id"])
	}
	elb := describeLoadBalancersOutput.LoadBalancers[0]

	elbArn := elb.LoadBalancerArn
	describeTagsOutput, err := elbClient.DescribeTags(ctx, &elasticloadbalancingv2.DescribeTagsInput{
		ResourceArns: []string{*elbArn},
	})
	if err != nil {
		return nil, err
	}
	if len(describeTagsOutput.TagDescriptions) == 0 {
		return nil, fmt.Errorf("Failed to get tags from ELB %s on account %s", elbName, labels["account_id"])
	}

	elbTags := make(map[string]string)
	for _, tag := range describeTagsOutput.TagDescriptions[0].Tags {
		elbTags[*tag.Key] = *tag.Value
	}

	return elbTags, nil
}

func filterElbTags(elbTags map[string]string) {
	for key := range elbTags {
		if _, ok := elbTagsAsLabels[key]; !ok {
			delete(elbTags, key)
		}
	}
}

func addToElbTagsLabelSet(labelSet model.LabelSet, name string, value string, tagKey string) error {
	labelName := model.LabelName(name)
	if !labelName.IsValid() {
		return fmt.Errorf("Could not define labels from tag %s: invalid label name %s", tagKey, name)
	}
	labelValue := model.LabelValue(value)
	if !labelValue.IsValid() {
		return fmt.Errorf("Could not define labels from tag %s: invalid label value %s", tagKey, value)
	}
	labelSet[model.LabelName(name)] = model.LabelValue(value)
	return nil
}

func getElbTagsLabelSet(ctx context.Context, labels map[string]string, log *log.Logger) (model.LabelSet, error) {
	elbTags, err := getELBTags(ctx, labels)
	if err != nil {
		return nil, err
	}
	filterElbTags(elbTags)

	elbTagsLabelSet := model.LabelSet{}
	for tagKey, tagValue := range elbTags {
		elbTagAsLabels := elbTagsAsLabels[tagKey]
		if elbTagAsLabels[0] == '/' && elbTagAsLabels[len(elbTagAsLabels)-1] == '/' {
			labelsValuesRE, err := regexp.Compile(elbTagAsLabels[1 : len(elbTagAsLabels)-1])
			if err != nil {
				return nil, fmt.Errorf("Could not define labels from tag %s: invalid regular expression %s", tagKey, elbTagAsLabels)
			}

			labelsNames := labelsValuesRE.SubexpNames()
			labelsValues := labelsValuesRE.FindStringSubmatch(tagValue)
			for i, labelName := range labelsNames {
				if i >= len(labelsValues) {
					level.Warn(*log).Log("msg", fmt.Sprintf("No match found for label %s in tag %s", labelName, tagKey))
					break
				}
				if i != 0 && labelName == "" {
					return nil, fmt.Errorf("Could not define labels from tag %s: capture group must be named", tagKey)
				}
				if i != 0 {
					if err := addToElbTagsLabelSet(elbTagsLabelSet, labelName, labelsValues[i], tagKey); err != nil {
						return nil, err
					}
				}
			}
			continue
		}

		if err := addToElbTagsLabelSet(elbTagsLabelSet, elbTagsAsLabels[tagKey], tagValue, tagKey); err != nil {
			return nil, err
		}
	}

	return elbTagsLabelSet, nil
}

func processS3Event(ctx context.Context, ev *events.S3Event, pc Client, log *log.Logger, streamDesiredRate float64) error {
	batch, err := newBatch(ctx, pc, streamDesiredRate)
	if err != nil {
		return err
	}
	for _, record := range ev.Records {
		labels, err := getLabels(record)
		if err != nil {
			return err
		}

		elbTagsLabelSet := model.LabelSet{}
		if labels["type"] == LB_LOG_TYPE && len(elbTagsAsLabels) > 0 {
			level.Info(*log).Log("msg", fmt.Sprintf("fetching elb tags: %s", labels["src"]))
			elbTagsLabelSet, err = getElbTagsLabelSet(ctx, labels, log)
			if err != nil {
				return err
			}
		}

		level.Info(*log).Log("msg", fmt.Sprintf("fetching s3 file: %s", labels["key"]))
		s3Client, err := getS3Client(ctx, labels["bucket_region"])
		if err != nil {
			return err
		}
		obj, err := s3Client.GetObject(ctx,
			&s3.GetObjectInput{
				Bucket:              aws.String(labels["bucket"]),
				Key:                 aws.String(labels["key"]),
				ExpectedBucketOwner: aws.String(labels["bucketOwner"]),
			})
		if err != nil {
			return fmt.Errorf("Failed to get object %s from bucket %s on account %s\n, %s", labels["key"], labels["bucket"], labels["bucketOwner"], err)
		}
		err = parseS3Log(ctx, batch, labels, elbTagsLabelSet, obj.Body)
		if err != nil {
			return err
		}
	}

	err = pc.sendToPromtail(ctx, batch)
	if err != nil {
		return err
	}

	return nil
}
