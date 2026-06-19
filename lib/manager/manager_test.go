// Copyright 2024 Google Inc. All Rights Reserved.
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

package manager

import (
	"testing"
	"time"

	"github.com/google/cadvisor/lib/cache/memory"
	"github.com/google/cadvisor/lib/container"
	info "github.com/google/cadvisor/lib/model"

	"k8s.io/utils/clock"
)

// mockHandler is a minimal container.ContainerHandler for exercising the
// manager's query/seam surface without the full (pruned) mock packages.
type mockHandler struct {
	ref  info.ContainerReference
	spec info.ContainerSpec
}

func (h *mockHandler) ContainerReference() (info.ContainerReference, error) { return h.ref, nil }
func (h *mockHandler) GetSpec() (info.ContainerSpec, error)                 { return h.spec, nil }
func (h *mockHandler) GetStats() (*info.ContainerStats, error) {
	return &info.ContainerStats{Timestamp: time.Now()}, nil
}
func (h *mockHandler) ListContainers(container.ListType) ([]info.ContainerReference, error) {
	return nil, nil
}
func (h *mockHandler) ListProcesses(container.ListType) ([]int, error) { return nil, nil }
func (h *mockHandler) GetCgroupPath(string) (string, error)            { return "/", nil }
func (h *mockHandler) GetContainerLabels() map[string]string           { return nil }
func (h *mockHandler) GetContainerIPAddress() string                   { return "" }
func (h *mockHandler) GetExitCode() (int, error)                       { return 0, nil }
func (h *mockHandler) Exists() bool                                    { return true }
func (h *mockHandler) Cleanup()                                        {}
func (h *mockHandler) Start()                                          {}
func (h *mockHandler) Type() container.ContainerType                   { return container.ContainerTypeRaw }

func newTestManager() *manager {
	return &manager{memoryCache: memory.New(time.Minute, nil)}
}

// addContainer registers a tracked container backed by a mock handler and seeds
// one stats sample so the query methods return data.
func (m *manager) addContainer(t *testing.T, name, namespace string) *containerData {
	t.Helper()
	h := &mockHandler{
		ref:  info.ContainerReference{Name: name, Namespace: namespace},
		spec: info.ContainerSpec{HasCpu: true},
	}
	cont, err := newContainerData(name, m.memoryCache, h, time.Minute, false, clock.RealClock{})
	if err != nil {
		t.Fatalf("newContainerData(%q): %v", name, err)
	}
	if err := m.memoryCache.AddStats(&info.ContainerInfo{ContainerReference: h.ref}, &info.ContainerStats{Timestamp: time.Now()}); err != nil {
		t.Fatalf("AddStats(%q): %v", name, err)
	}
	m.containers.Store(namespacedContainerName{Namespace: namespace, Name: name}, cont)
	return cont
}

func TestExists(t *testing.T) {
	m := newTestManager()
	m.addContainer(t, "/x", "")
	if !m.Exists("/x") {
		t.Errorf("Exists(/x) = false, want true")
	}
	if m.Exists("/y") {
		t.Errorf("Exists(/y) = true, want false")
	}
}

func TestGetContainerInfo(t *testing.T) {
	m := newTestManager()
	m.addContainer(t, "/x", "")
	cinfo, err := m.GetContainerInfo("/x", &info.ContainerInfoRequest{NumStats: 10})
	if err != nil {
		t.Fatalf("GetContainerInfo: %v", err)
	}
	if cinfo.Name != "/x" {
		t.Errorf("Name = %q, want /x", cinfo.Name)
	}
	if !cinfo.Spec.HasCpu {
		t.Errorf("Spec.HasCpu = false, want true")
	}
	if len(cinfo.Stats) == 0 {
		t.Errorf("Stats empty, want >=1")
	}
	if _, err := m.GetContainerInfo("/missing", &info.ContainerInfoRequest{}); err == nil {
		t.Errorf("GetContainerInfo(/missing) err = nil, want unknown-container error")
	}
}

func TestAllDockerContainers(t *testing.T) {
	m := newTestManager()
	m.addContainer(t, "/docker/abc", DockerNamespace)
	m.addContainer(t, "/x", "") // non-docker, must be excluded
	docker, err := m.AllDockerContainers(&info.ContainerInfoRequest{NumStats: 1})
	if err != nil {
		t.Fatalf("AllDockerContainers: %v", err)
	}
	if _, ok := docker["/docker/abc"]; !ok {
		t.Errorf("docker container /docker/abc missing from %v", keys(docker))
	}
	if _, ok := docker["/x"]; ok {
		t.Errorf("non-docker /x leaked into AllDockerContainers")
	}
}

// TestGetDerivedStatsNotEnabled exercises the summary seam default: with no
// SummaryReaderFactory wired (the kubelet case) each container reports "not
// enabled" rather than panicking.
func TestGetDerivedStatsNotEnabled(t *testing.T) {
	m := newTestManager()
	m.addContainer(t, "/x", "")
	_, err := m.GetDerivedStats("/x", info.RequestOptions{IdType: info.TypeName, Count: 1})
	if err == nil {
		t.Errorf("GetDerivedStats err = nil, want 'not enabled' (no summary reader wired)")
	}
}

// TestGetProcessListSeam exercises the ps seam: nil provider yields an empty
// list; a wired provider is invoked with the container's identity.
func TestGetProcessListSeam(t *testing.T) {
	m := newTestManager()
	m.addContainer(t, "/x", "")

	ProcessListProvider = nil
	got, err := m.GetProcessList("/x", info.RequestOptions{IdType: info.TypeName, Count: 1})
	if err != nil || len(got) != 0 {
		t.Errorf("GetProcessList(nil provider) = (%v, %v), want ([], nil)", got, err)
	}

	var gotName string
	ProcessListProvider = func(name string, isRoot bool, _ string, _ bool) ([]info.ProcessInfo, error) {
		gotName = name
		return []info.ProcessInfo{{Pid: 42}}, nil
	}
	defer func() { ProcessListProvider = nil }()
	got, err = m.GetProcessList("/x", info.RequestOptions{IdType: info.TypeName, Count: 1})
	if err != nil {
		t.Fatalf("GetProcessList(provider): %v", err)
	}
	if len(got) != 1 || got[0].Pid != 42 {
		t.Errorf("GetProcessList = %v, want one proc pid=42", got)
	}
	if gotName != "/x" {
		t.Errorf("provider got container %q, want /x", gotName)
	}
}

func keys(m map[string]info.ContainerInfo) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
