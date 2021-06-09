//
// Copyright 2019-2020 Nestybox, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//

package implementations

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/nestybox/sysbox-fs/domain"
)

//
// /proc/sys/net/unix handler
//
// Emulated resources:
//
// * /proc/sys/net/unix/max_dgram_qlen
//
type ProcSysNetUnix struct {
	domain.HandlerBase
}

var ProcSysNetUnix_Handler = &ProcSysNetUnix{
	domain.HandlerBase{
		Name: "ProcSysNetUnix",
		Path: "/proc/sys/net/unix",
		EmuResourceMap: map[string]domain.EmuResource{
			"max_dgram_qlen": {
				Kind:    domain.FileEmuResource,
				Mode:    os.FileMode(uint32(0644)),
				Enabled: true,
			},
		},
	},
}

func (h *ProcSysNetUnix) Lookup(
	n domain.IOnodeIface,
	req *domain.HandlerRequest) (os.FileInfo, error) {

	var resource = n.Name()

	logrus.Debugf("Executing Lookup() for Req ID=%#x, %v handler, resource %s",
		req.ID, h.Name, resource)

	// Return an artificial fileInfo if looked-up element matches any of the
	// emulated nodes.
	if v, ok := h.EmuResourceMap[resource]; ok {
		info := &domain.FileInfo{
			Fname:    resource,
			Fmode:    v.Mode,
			FmodTime: time.Now(),
		}

		return info, nil
	}

	// If looked-up element hasn't been found by now, let's look into the actual
	// sys container rootfs.
	procSysCommonHandler, ok := h.Service.FindHandler("/proc/sys/")
	if !ok {
		return nil, fmt.Errorf("No /proc/sys/ handler found")
	}

	return procSysCommonHandler.Lookup(n, req)
}

func (h *ProcSysNetUnix) Open(
	n domain.IOnodeIface,
	req *domain.HandlerRequest) error {

	var resource = n.Name()

	logrus.Debugf("Executing Open() for Req ID=%#x, %v handler, resource %s",
		req.ID, h.Name, resource)

	switch resource {
	case "max_dgram_qlen":
		return nil
	}

	procSysCommonHandler, ok := h.Service.FindHandler("/proc/sys/")
	if !ok {
		return fmt.Errorf("No /proc/sys/ handler found")
	}

	return procSysCommonHandler.Open(n, req)
}

func (h *ProcSysNetUnix) Read(
	n domain.IOnodeIface,
	req *domain.HandlerRequest) (int, error) {

	var resource = n.Name()

	logrus.Debugf("Executing Read() for Req ID=%#x, %v handler, resource %s",
		req.ID, h.Name, resource)

	// We are dealing with a single boolean element being read, so we can save
	// some cycles by returning right away if offset is any higher than zero.
	if req.Offset > 0 {
		return 0, io.EOF
	}

	switch resource {
	case "max_dgram_qlen":
		return readFileInt(h, n, req)
	}

	// Refer to generic handler if no node match is found above.
	procSysCommonHandler, ok := h.Service.FindHandler("/proc/sys/")
	if !ok {
		return 0, fmt.Errorf("No /proc/sys/ handler found")
	}

	return procSysCommonHandler.Read(n, req)
}

func (h *ProcSysNetUnix) Write(
	n domain.IOnodeIface,
	req *domain.HandlerRequest) (int, error) {

	var resource = n.Name()

	logrus.Debugf("Executing Write() for Req ID=%#x, %v handler, resource %s",
		req.ID, h.Name, resource)

	switch resource {
	case "max_dgram_qlen":
		return writeFileMaxInt(h, n, req, true)
	}

	// Refer to generic handler if no node match is found above.
	procSysCommonHandler, ok := h.Service.FindHandler("/proc/sys/")
	if !ok {
		return 0, fmt.Errorf("No /proc/sys/ handler found")
	}

	return procSysCommonHandler.Write(n, req)
}

func (h *ProcSysNetUnix) ReadDirAll(
	n domain.IOnodeIface,
	req *domain.HandlerRequest) ([]os.FileInfo, error) {

	var resource = n.Name()

	logrus.Debugf("Executing ReadDirAll() for Req ID=%#x, %v handler, resource %s",
		req.ID, h.Name, resource)

	var fileEntries []os.FileInfo

	// Also collect procfs entries as seen within container's namespaces.
	procSysCommonHandler, ok := h.Service.FindHandler("/proc/sys/")
	if !ok {
		return nil, fmt.Errorf("No /proc/sys/ handler found")
	}
	commonNeigh, err := procSysCommonHandler.ReadDirAll(n, req)
	if err == nil {
		for _, entry := range commonNeigh {
			fileEntries = append(fileEntries, entry)
		}
	}

	return fileEntries, nil
}

func (h *ProcSysNetUnix) GetName() string {
	return h.Name
}

func (h *ProcSysNetUnix) GetPath() string {
	return h.Path
}

func (h *ProcSysNetUnix) GetService() domain.HandlerServiceIface {
	return h.Service
}

func (h *ProcSysNetUnix) GetResourceMap() map[string]domain.EmuResource {
	return h.EmuResourceMap
}

func (h *ProcSysNetUnix) GetResourceMutex(s string) *sync.Mutex {
	resource, ok := h.EmuResourceMap[s]
	if !ok {
		return nil
	}

	return &resource.Mutex
}

func (h *ProcSysNetUnix) SetService(hs domain.HandlerServiceIface) {
	h.Service = hs
}
