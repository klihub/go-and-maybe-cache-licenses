// Copyright 2026 Google Inc. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

// collect returns a reporter that appends its output to a slice.
func collect(what string) (*progressReporter, *[]string) {
	var lines []string
	r := newProgressReporter(what, func(format string, args ...interface{}) {
		lines = append(lines, fmt.Sprintf(format, args...))
	})
	return r, &lines
}

// TestProgressReportsAtIntervals pins the property that keeps a progress
// report readable in a log: one line per tenth of the work, not one per item.
func TestProgressReportsAtIntervals(t *testing.T) {
	r, lines := collect("classified")
	const total = 381
	for i := 1; i <= total; i++ {
		r.report(i, total)
	}

	if len(*lines) != 10 {
		t.Fatalf("got %d progress lines for %d items, want 10:\n%s",
			len(*lines), total, strings.Join(*lines, "\n"))
	}
	for i, want := range []int{10, 20, 30, 40, 50, 60, 70, 80, 90, 100} {
		if !strings.Contains((*lines)[i], fmt.Sprintf("%d%%", want)) {
			t.Errorf("line %d = %q, want it to report %d%%", i, (*lines)[i], want)
		}
	}
	last := (*lines)[len(*lines)-1]
	for _, want := range []string{"classified", "100%", "381/381"} {
		if !strings.Contains(last, want) {
			t.Errorf("final line %q is missing %q", last, want)
		}
	}
}

// TestProgressAlwaysReportsCompletion covers small totals, where every step
// crosses several percent at once and the last one must still be reported.
func TestProgressAlwaysReportsCompletion(t *testing.T) {
	for _, total := range []int{1, 2, 3, 7} {
		r, lines := collect("resolved")
		for i := 1; i <= total; i++ {
			r.report(i, total)
		}
		if len(*lines) == 0 {
			t.Errorf("total %d produced no progress lines", total)
			continue
		}
		last := (*lines)[len(*lines)-1]
		if !strings.Contains(last, "100%") {
			t.Errorf("total %d: final line %q does not report 100%%", total, last)
		}
		if len(*lines) > total {
			t.Errorf("total %d produced %d lines, more than there were items", total, len(*lines))
		}
	}
}

func TestProgressReportsNothingForNoWork(t *testing.T) {
	r, lines := collect("classified")
	r.report(0, 0)
	if len(*lines) != 0 {
		t.Errorf("got %d lines for an empty phase, want none: %v", len(*lines), *lines)
	}
}

// TestProgressIsSafeForConcurrentUse covers the classification fan-out, which
// reports from several goroutines at once.
func TestProgressIsSafeForConcurrentUse(t *testing.T) {
	var mu sync.Mutex
	var count int
	r := newProgressReporter("classified", func(string, ...interface{}) {
		mu.Lock()
		count++
		mu.Unlock()
	})

	const total = 200
	var wg sync.WaitGroup
	var seq sync.Mutex
	done := 0
	for range total {
		wg.Add(1)
		go func() {
			defer wg.Done()
			seq.Lock()
			done++
			n := done
			seq.Unlock()
			r.report(n, total)
		}()
	}
	wg.Wait()

	if count == 0 || count > 10 {
		t.Errorf("got %d progress lines under concurrency, want between 1 and 10", count)
	}
}
