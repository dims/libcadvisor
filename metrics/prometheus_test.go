// Copyright 2014 Google Inc. All Rights Reserved.
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

package metrics

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/dims/libcadvisor/container"
	info "github.com/dims/libcadvisor/model"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	clock "k8s.io/utils/clock/testing"
)

var now = clock.NewFakeClock(time.Unix(1395066363, 0))

func TestPrometheusCollector(t *testing.T) {
	c := NewPrometheusCollector(testSubcontainersInfoProvider{}, func(container *info.ContainerInfo) map[string]string {
		s := DefaultContainerLabels(container)
		s["zone.name"] = "hello"
		return s
	}, container.AllMetrics, now, info.RequestOptions{})
	reg := prometheus.NewRegistry()
	reg.MustRegister(c)

	testPrometheusCollector(t, reg, "testdata/prometheus_metrics")
}

func TestPrometheusCollectorWithWhiteList(t *testing.T) {
	c := NewPrometheusCollector(testSubcontainersInfoProvider{}, func(container *info.ContainerInfo) map[string]string {
		whitelistedLabels := []string{
			"no_one_match",
		}
		containerLabelFunc := BaseContainerLabels(whitelistedLabels)
		s := containerLabelFunc(container)
		s["zone.name"] = "hello"
		return s
	}, container.AllMetrics, now, info.RequestOptions{})
	reg := prometheus.NewRegistry()
	reg.MustRegister(c)

	testPrometheusCollector(t, reg, "testdata/prometheus_metrics_whitelist_filtered")
}

func testPrometheusCollector(t *testing.T, gatherer prometheus.Gatherer, metricsFile string) {
	wantMetrics, err := os.Open(metricsFile)
	if err != nil {
		t.Fatalf("unable to read input test file %s", metricsFile)
	}

	err = testutil.GatherAndCompare(gatherer, wantMetrics)
	if err != nil {
		t.Fatalf("Metric comparison failed: %s", err)
	}
}

func TestPrometheusCollector_scrapeFailure(t *testing.T) {
	provider := &erroringSubcontainersInfoProvider{
		successfulProvider: testSubcontainersInfoProvider{},
		shouldFail:         true,
	}

	c := NewPrometheusCollector(provider, func(container *info.ContainerInfo) map[string]string {
		s := DefaultContainerLabels(container)
		s["zone.name"] = "hello"
		return s
	}, container.AllMetrics, now, info.RequestOptions{})
	reg := prometheus.NewRegistry()
	reg.MustRegister(c)

	testPrometheusCollector(t, reg, "testdata/prometheus_metrics_failure")

	provider.shouldFail = false

	testPrometheusCollector(t, reg, "testdata/prometheus_metrics")
}

func TestNewPrometheusCollectorWithRequestOptions(t *testing.T) {
	p := mockInfoProvider{}
	opts := info.RequestOptions{
		IdType: "docker",
	}
	c := NewPrometheusCollector(&p, mockLabelFunc, container.AllMetrics, now, opts)
	ch := make(chan prometheus.Metric, 10)
	c.Collect(ch)
	assert.Equal(t, p.options, opts)
}

type mockInfoProvider struct {
	options info.RequestOptions
}

func (m *mockInfoProvider) GetRequestedContainersInfo(containerName string, options info.RequestOptions) (map[string]*info.ContainerInfo, error) {
	m.options = options
	return map[string]*info.ContainerInfo{}, nil
}

func (m *mockInfoProvider) GetVersionInfo() (*info.VersionInfo, error) {
	return nil, errors.New("not supported")
}

func (m *mockInfoProvider) GetMachineInfo() (*info.MachineInfo, error) {
	return nil, errors.New("not supported")
}

func mockLabelFunc(*info.ContainerInfo) map[string]string {
	return map[string]string{}
}

func TestGetContainerHealthState(t *testing.T) {
	testCases := []struct {
		name           string
		containerStats *info.ContainerStats
		expectedValue  float64
	}{
		{name: "healthy", expectedValue: 1.0, containerStats: &info.ContainerStats{Health: info.Health{Status: "healthy"}}},
		{name: "unhealthy", expectedValue: 0.0, containerStats: &info.ContainerStats{Health: info.Health{Status: "unhealthy"}}},
		{name: "starting", expectedValue: 0.0, containerStats: &info.ContainerStats{Health: info.Health{Status: "unknown"}}},
		{name: "empty", expectedValue: -1.0, containerStats: &info.ContainerStats{}},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			metricVals := getContainerHealthState(tc.containerStats)
			assert.Equal(t, 1, len(metricVals))
			assert.Equal(t, tc.expectedValue, metricVals[0].value)
		})
	}
}

func TestIOCostMetrics(t *testing.T) {
	containerStats := &info.ContainerStats{
		Timestamp: time.Unix(1395066363, 0),
		DiskIo: info.DiskIoStats{
			IoCostUsage: []info.PerDiskStats{{
				Device: "sda1",
				Major:  8,
				Minor:  1,
				Stats:  map[string]uint64{"Count": 1500000},
			}},
			IoCostWait: []info.PerDiskStats{{
				Device: "sda1",
				Major:  8,
				Minor:  1,
				Stats:  map[string]uint64{"Count": 2500000},
			}},
			IoCostIndebt: []info.PerDiskStats{{
				Device: "sda1",
				Major:  8,
				Minor:  1,
				Stats:  map[string]uint64{"Count": 500000},
			}},
			IoCostIndelay: []info.PerDiskStats{{
				Device: "sda1",
				Major:  8,
				Minor:  1,
				Stats:  map[string]uint64{"Count": 750000},
			}},
		},
	}

	testCases := []struct {
		name          string
		stats         []info.PerDiskStats
		expectedValue float64
	}{
		{
			name:          "IoCostUsage",
			stats:         containerStats.DiskIo.IoCostUsage,
			expectedValue: 1.5,
		},
		{
			name:          "IoCostWait",
			stats:         containerStats.DiskIo.IoCostWait,
			expectedValue: 2.5,
		},
		{
			name:          "IoCostIndebt",
			stats:         containerStats.DiskIo.IoCostIndebt,
			expectedValue: 0.5,
		},
		{
			name:          "IoCostIndelay",
			stats:         containerStats.DiskIo.IoCostIndelay,
			expectedValue: 0.75,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			values := ioValues(
				tc.stats, "Count", asMicrosecondsToSeconds,
				[]info.FsStats{}, nil,
				containerStats.Timestamp,
			)
			assert.Equal(t, 1, len(values))
			assert.Equal(t, tc.expectedValue, values[0].value)
			assert.Equal(t, []string{"sda1"}, values[0].labels)
		})
	}
}

func TestCPUBurstMetrics(t *testing.T) {
	containerStats := &info.ContainerStats{
		Timestamp: time.Unix(1395066363, 0),
		Cpu: info.CpuStats{
			CFS: info.CpuCFS{
				BurstsPeriods: 25,
				BurstTime:     500000000,
			},
		},
	}

	testCases := []struct {
		name          string
		getValue      func() float64
		expectedValue float64
	}{
		{
			name:          "BurstsPeriods",
			getValue:      func() float64 { return float64(containerStats.Cpu.CFS.BurstsPeriods) },
			expectedValue: 25.0,
		},
		{
			name:          "BurstTime",
			getValue:      func() float64 { return float64(containerStats.Cpu.CFS.BurstTime) / float64(time.Second) },
			expectedValue: 0.5,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := tc.getValue()
			assert.Equal(t, tc.expectedValue, result)
		})
	}
}
