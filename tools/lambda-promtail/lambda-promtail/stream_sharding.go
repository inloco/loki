package main

import (
	"fmt"
	"math"
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
	dataRateTracker   *DataRateTracker
	streamDesiredRate float64
	shards            int64
	rand              *rand.Rand
	logger            *log.Logger
}

func NewStreamSharding(streamDesiredRate float64, streamRateTrackerWindowSize time.Duration, logger *log.Logger) *StreamSharding {
	return &StreamSharding{
		dataRateTracker:   NewDataRateTracker(streamRateTrackerWindowSize),
		streamDesiredRate: streamDesiredRate,
		rand:              rand.New(rand.NewSource(time.Now().UnixNano())),
		shards:            1,
		logger:            logger,
	}
}

func (s *StreamSharding) Update(dataSize int64) {
	s.dataRateTracker.Update(dataSize)

	rate := s.dataRateTracker.GetRate()
	s.shards = int64(math.Ceil(rate / 1048576 / s.streamDesiredRate))

	level.Debug(*s.logger).Log("msg", fmt.Sprintf("Updated transmission rate (%dms window): %.0fB/s (%.2fMB/s desired)", s.dataRateTracker.windowSize.Milliseconds(), rate, s.streamDesiredRate))
	level.Debug(*s.logger).Log("msg", fmt.Sprintf("Updated stream shards: %d", s.shards))
}

func (s *StreamSharding) GetRandomShard() int64 {
	if s.shards == 0 {
		return 1
	}
	return 1 + s.rand.Int63n(s.shards)
}
