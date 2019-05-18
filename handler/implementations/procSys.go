package implementations

import (
	"fmt"
	"io"
	"log"
	"os"
	"syscall"

	"github.com/nestybox/sysvisor/sysvisor-fs/domain"
)

//
// /proc/sys Handler
//
type ProcSysHandler struct {
	Name      string
	Path      string
	Enabled   bool
	Cacheable bool
	Service   domain.HandlerService
}

//func (h *ProcSysHandler) Lookup(n domain.IOnode, i domain.Inode) (*syscall.Stat_t, error) {
func (h *ProcSysHandler) Lookup(n domain.IOnode, pid uint32) (os.FileInfo, error) {

	log.Printf("Executing Lookup() method on %v handler", h.Name)

	return n.Stat()
}

func (h *ProcSysHandler) Getattr(n domain.IOnode, pid uint32) (*syscall.Stat_t, error) {

	log.Printf("Executing Getattr() method on %v handler", h.Name)

	// Let's refer to the commonHandler for this task.
	commonHandler, ok := h.Service.FindHandler("commonHandler")
	if !ok {
		return nil, fmt.Errorf("No commonHandler found")
	}

	return commonHandler.Getattr(n, pid)
}

func (h *ProcSysHandler) Open(node domain.IOnode) error {

	log.Printf("Executing %v open() method", h.Name)

	return nil
}

func (h *ProcSysHandler) Close(node domain.IOnode) error {

	log.Printf("Executing Close() method on %v handler", h.Name)

	return nil
}

func (h *ProcSysHandler) Read(n domain.IOnode, pid uint32,
	buf []byte, off int64) (int, error) {

	log.Printf("Executing %v read() method", h.Name)

	if off > 0 {
		return 0, io.EOF
	}

	return 0, nil
}

func (h *ProcSysHandler) Write(n domain.IOnode, pid uint32,
	buf []byte) (int, error) {

	return 0, nil
}

func (h *ProcSysHandler) ReadDirAll(n domain.IOnode, pid uint32) ([]os.FileInfo, error) {

	log.Printf("Executing ReadDirAll() method on %v handler", h.Name)

	commonHandler, ok := h.Service.FindHandler("commonHandler")
	if !ok {
		return nil, fmt.Errorf("No commonHandler found")
	}

	return commonHandler.ReadDirAll(n, pid)
}

func (h *ProcSysHandler) GetName() string {
	return h.Name
}

func (h *ProcSysHandler) GetPath() string {
	return h.Path
}

func (h *ProcSysHandler) GetEnabled() bool {
	return h.Enabled
}

func (h *ProcSysHandler) GetService() domain.HandlerService {
	return h.Service
}

func (h *ProcSysHandler) SetEnabled(val bool) {
	h.Enabled = val
}

func (h *ProcSysHandler) SetService(hs domain.HandlerService) {
	h.Service = hs
}
