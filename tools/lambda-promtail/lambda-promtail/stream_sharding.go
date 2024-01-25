package main

import (
	"fmt"
	"math/rand"
	"time"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
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
	stream            string
	dataRateTracker   *DataRateTracker
	streamDesiredRate float64
	shards            int64
	rand              *rand.Rand
	logger            *log.Logger
}

func NewStreamSharding(stream string, streamDesiredRate float64, streamRateTrackerWindowSize time.Duration, logger *log.Logger) *StreamSharding {
	return &StreamSharding{
		stream:            stream,
		dataRateTracker:   NewDataRateTracker(streamRateTrackerWindowSize),
		streamDesiredRate: streamDesiredRate,
		rand:              rand.New(rand.NewSource(time.Now().UnixNano())),
		shards:            1,
		logger:            logger,
	}
}

func (s *StreamSharding) Update(dataSize int64) {
	s.dataRateTracker.Update(dataSize)
	streamRate := s.dataRateTracker.GetRate()

	level.Info(*s.logger).Log("msg", fmt.Sprintf("Updated rate for stream %s: %fB/s (%fMB/s desired)", s.stream, streamRate, s.streamDesiredRate))

	s.shards = int64((s.dataRateTracker.GetRate() / (1024 * 1024) / s.streamDesiredRate) + 0.5)

	level.Info(*s.logger).Log("msg", fmt.Sprintf("Updated shards for stream %s: %d", s.stream, s.shards))
}

func (s *StreamSharding) GetRandomShard() int64 {
	return 1 + s.rand.Int63n(s.shards)
}
