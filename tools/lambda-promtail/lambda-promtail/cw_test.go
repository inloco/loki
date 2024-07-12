package main

import (
	"context"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/require"
)

func Test_parseCWEvent(t *testing.T) {
	newBatch := func(ctx context.Context) *batch {
		batch, _ := newBatch(ctx, nil, 0, 0, NewLogger("test"))
		return batch
	}

	tests := []struct {
		name           string
		b              *batch
		expectedStream string
		keepStream     bool
	}{
		{
			name:           "cloudwatch",
			b:              newBatch(context.Background()),
			expectedStream: `{__aws_cloudwatch_log_group="testLogGroup", __aws_cloudwatch_owner="123456789123", __aws_log_type="cloudwatch", __lambda_promtail_stream_shard__="1"}`,
			keepStream:     false,
		},
		{
			name:           "cloudwatch_keepStream",
			b:              newBatch(context.Background()),
			expectedStream: `{__aws_cloudwatch_log_group="testLogGroup", __aws_cloudwatch_log_stream="testLogStream", __aws_cloudwatch_owner="123456789123", __aws_log_type="cloudwatch", __lambda_promtail_stream_shard__="1"}`,
			keepStream:     true,
		},
	}

	for _, tt := range tests {
		// Docs: https://docs.aws.amazon.com/lambda/latest/dg/services-cloudwatchlogs.html
		// Example CloudWatchLogEvent copied from https://github.com/aws/aws-lambda-go/blob/main/events/cloudwatch_logs_test.go
		cwevent := &events.CloudwatchLogsEvent{
			AWSLogs: events.CloudwatchLogsRawData{
				Data: "H4sIAAAAAAAAAHWPwQqCQBCGX0Xm7EFtK+smZBEUgXoLCdMhFtKV3akI8d0bLYmibvPPN3wz00CJxmQnTO41whwWQRIctmEcB6sQbFC3CjW3XW8kxpOpP+OC22d1Wml1qZkQGtoMsScxaczKN3plG8zlaHIta5KqWsozoTYw3/djzwhpLwivWFGHGpAFe7DL68JlBUk+l7KSN7tCOEJ4M3/qOI49vMHj+zCKdlFqLaU2ZHV2a4Ct/an0/ivdX8oYc1UVX860fQDQiMdxRQEAAA==",
			},
		}

		t.Run(tt.name, func(t *testing.T) {
			batchSize = 131072 // Set large enough we don't send to promtail
			keepStream = tt.keepStream
			err := parseCWEvent(context.Background(), tt.b, cwevent)
			if err != nil {
				t.Error(err)
			}
			require.Len(t, tt.b.streams, 1)
			stream, ok := tt.b.streams[tt.expectedStream]
			require.True(t, ok, "batch does not contain stream: %s", tt.expectedStream)
			require.NotNil(t, stream)
		})
	}
}
