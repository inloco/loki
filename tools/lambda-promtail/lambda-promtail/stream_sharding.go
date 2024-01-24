package main

import (
	"math/rand"
	"time"
)

const (
	STREAM_RATE_TRACKER_WINDOW_SIZE = 60 * time.Second
)

type DataRateTracker struct {
	windowSize time.Duration
	startTime  time.Time
	totalData  int64
	rate       float64
}

func NewDataRateTracker(windowSize time.Duration) *DataRateTracker {
	return &DataRateTracker{
		windowSize: windowSize,
		startTime:  time.Now(),
		totalData:  0,
	}
}

func (tracker *DataRateTracker) Update(dataSize int64) {
	tracker.totalData += dataSize
	elapsedTime := time.Since(tracker.startTime).Seconds()
	if elapsedTime >= tracker.windowSize.Seconds() {
		tracker.rate = float64(tracker.totalData) / elapsedTime / (1024 * 1024)
		tracker.totalData = 0
	}
}

func (tracker *DataRateTracker) GetRate() float64 {
	return tracker.rate
}

type StreamSharding struct {
	dataRateTracker   *DataRateTracker
	streamDesiredRate float64
	shards            int64
	rand              *rand.Rand
}

func NewStreamSharding(streamDesiredRate float64) *StreamSharding {
	return &StreamSharding{
		dataRateTracker:   NewDataRateTracker(STREAM_RATE_TRACKER_WINDOW_SIZE),
		streamDesiredRate: streamDesiredRate,
		rand:              rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func (sharding *StreamSharding) Update(dataSize int64) {
	sharding.dataRateTracker.Update(dataSize)
	sharding.shards = int64(sharding.dataRateTracker.GetRate() / sharding.streamDesiredRate)
}

func (sharding *StreamSharding) GetRandomShard() int64 {
	return 1 + sharding.rand.Int63n(sharding.shards)
}
