package main

import (
	"math/rand"
	"time"
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
		rate:       0,
	}
}

func (tracker *DataRateTracker) Update(dataSize int64) {
	tracker.totalData += dataSize
	elapsedTime := time.Since(tracker.startTime).Seconds()
	if elapsedTime >= tracker.windowSize.Seconds() {
		tracker.rate = float64(tracker.totalData) / elapsedTime
		tracker.startTime = time.Now()
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

func NewStreamSharding(streamDesiredRate float64, streamRateTrackerWindowSize time.Duration) *StreamSharding {
	return &StreamSharding{
		dataRateTracker:   NewDataRateTracker(streamRateTrackerWindowSize),
		streamDesiredRate: streamDesiredRate,
		rand:              rand.New(rand.NewSource(time.Now().UnixNano())),
		shards:            1,
	}
}

func (sharding *StreamSharding) Update(dataSize int64) {
	sharding.dataRateTracker.Update(dataSize)
	sharding.shards = int64((sharding.dataRateTracker.GetRate() / (1024 * 1024) / sharding.streamDesiredRate) + 0.5)
}

func (sharding *StreamSharding) GetRandomShard() int64 {
	return 1 + sharding.rand.Int63n(sharding.shards)
}
