// Copyright (C) 2019-2022, Lux Partners Limited. All rights reserved.
// See the file LICENSE for licensing terms.

package timer

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSingleStagedTimer(t *testing.T) {
	wg := sync.WaitGroup{}
	wg.Add(1)
	ticks := 1
	i := 0
	timer := NewStagedTimer(func() (time.Duration, bool) {
		defer wg.Done()
		i++
		return 0, false
	})
	go timer.Dispatch()

	timer.SetTimeoutIn(time.Millisecond)
	wg.Wait()
	require.Equal(t, i, ticks)
}

func TestMultiStageTimer(t *testing.T) {
	wg := sync.WaitGroup{}
	ticks := 3
	wg.Add(ticks)

	i := 0
	timer := NewStagedTimer(func() (time.Duration, bool) {
		defer wg.Done()
		i++
		return time.Millisecond, i < ticks
	})
	go timer.Dispatch()

	timer.SetTimeoutIn(time.Millisecond)
	wg.Wait()
	require.Equal(t, i, ticks)
}
