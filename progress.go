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
	"sync"

	"k8s.io/klog/v2"
)

// progress enables progress reporting for the slow phases of a license run.
var progress bool

func init() {
	rootCmd.PersistentFlags().BoolVar(&progress, "progress", false,
		"Report progress while classifying license files and resolving license URLs. "+
			"Both phases know how much work they have up front, so the report is a real "+
			"proportion rather than an indication of activity.")
}

// progressStep is the granularity of reporting, in percent. A line per item
// would be unreadable in a log, and a redrawn line needs a terminal.
const progressStep = 10

// progressReporter reports completion at percentage boundaries.
//
// It is safe for concurrent use: classification reports from an errgroup.
type progressReporter struct {
	what string
	logf func(format string, args ...interface{})

	mu       sync.Mutex
	reported int // highest percentage already reported
}

func newProgressReporter(what string, logf func(format string, args ...interface{})) *progressReporter {
	return &progressReporter{what: what, logf: logf}
}

// report notes that done of total items are finished, logging only when that
// crosses the next boundary, and always when the work is complete.
func (p *progressReporter) report(done, total int) {
	if p == nil || total <= 0 || done <= 0 {
		return
	}

	pct := done * 100 / total
	// Round down to a boundary so that a step crossing several at once, as
	// happens with small totals, reports the one it reached.
	bucket := pct / progressStep * progressStep
	if done >= total {
		bucket = 100
	}
	if bucket == 0 {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if bucket <= p.reported {
		return
	}
	p.reported = bucket
	p.logf("%s %d%% (%d/%d)", p.what, bucket, done, total)
}

// newRunProgress returns a reporter writing through klog, or nil when
// progress reporting is off. A nil reporter is safe to call.
func newRunProgress(what string) *progressReporter {
	if !progress {
		return nil
	}
	return newProgressReporter(what, func(format string, args ...interface{}) {
		klog.Infof(format, args...)
	})
}

// progressFor returns the callback form the licenses package expects, or nil
// when progress reporting is off.
func progressFor(what string) func(done, total int) {
	r := newRunProgress(what)
	if r == nil {
		return nil
	}
	return r.report
}
