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

package nsenter

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	_ "github.com/nestybox/sysbox-runc/libcontainer/nsenter"
	"github.com/nestybox/sysbox-runc/libcontainer/utils"
	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink/nl"
	"golang.org/x/sys/unix"

	"github.com/nestybox/sysbox-fs/domain"
	"github.com/nestybox/sysbox-fs/fuse"
	"github.com/nestybox/sysbox-fs/mount"
	"github.com/nestybox/sysbox-fs/process"
	"github.com/nestybox/sysbox-runc/libcontainer"
)

func init() {
	if len(os.Args) > 1 && os.Args[1] == "nsenter" {
		runtime.GOMAXPROCS(1)
		runtime.LockOSThread()
	}
}

// Pid struct. Utilized by sysbox-runc's nsexec code.
type pid struct {
	Pid           int `json:"pid"`
	PidFirstChild int `json:"pid_first"`
}

//
// NSenterEvent struct serves as a transport abstraction (envelope) to carry
// all the potential messages that can be exchanged between sysbox-fs master
// instance and secondary (forked) ones. These sysbox-fs' auxiliary instances
// are utilized to perform actions over namespaced resources, and as such,
// cannot be executed by sysbox-fs' main instance.
//
// Every bidirectional transaction is represented by an event structure
// (nsenterEvent), which holds both 'request' and 'response' messages, as well
// as the context necessary to complete any action demanding inter-namespace
// message exchanges.
//
type NSenterEvent struct {

	// Pid on behalf of which sysbox-fs is creating the nsenter event.
	Pid uint32 `json:"pid"`

	// namespace-types to attach to.
	Namespace *[]domain.NStype `json:"namespace"`

	// Request message to be sent.
	ReqMsg *domain.NSenterMessage `json:"request"`

	// Request message to be received.
	ResMsg *domain.NSenterMessage `json:"response"`

	// Sysbox-fs' spawned process carrying out the nsexec instruction.
	Process *os.Process `json:"process"`

	// IPC pipes among sysbox-fs parent / child processes.
	parentPipe *os.File

	// Asynchronous flag to tag events for which no response is expected.
	async bool

	// Zombie Reaper (for left-over nsenter child processes)
	reaper *zombieReaper

	// Backpointer to Nsenter service
	service *nsenterService
}

//
// Generic getter / setter methods.
//

func (e *NSenterEvent) SetRequestMsg(m *domain.NSenterMessage) {
	e.ReqMsg = m
}

func (e *NSenterEvent) GetRequestMsg() *domain.NSenterMessage {
	return e.ReqMsg
}

func (e *NSenterEvent) SetResponseMsg(m *domain.NSenterMessage) {
	e.ResMsg = m
}

func (e *NSenterEvent) GetResponseMsg() *domain.NSenterMessage {
	return e.ResMsg
}

func (e *NSenterEvent) GetProcessID() uint32 {
	return uint32(e.Process.Pid)
}

///////////////////////////////////////////////////////////////////////////////
//
// nsenterEvent methods below execute within the context of sysbox-fs' main
// instance, upon invocation of sysbox-fs' handler or seccomp-bpf logic.
//
///////////////////////////////////////////////////////////////////////////////

//
// Called by sysbox-fs handler routines to parse the response generated
// by sysbox-fs' grand-child processes.
//
func (e *NSenterEvent) processResponse(pipe io.Reader) error {

	// Raw message payload to aid in decoding generic messages (see below
	// explanation).
	var payload json.RawMessage
	nsenterMsg := domain.NSenterMessage{
		Payload: &payload,
	}

	// Decode received msg header to help us determine the payload type.
	// Received message will be decoded in two phases. The decode instruction
	// below help us determine the message-type being received. Based on the
	// obtained type, we are able to decode the payload generated by the
	// remote-end. This second step is executed as part of a subsequent
	// unmarshal instruction (see further below).
	if err := json.NewDecoder(pipe).Decode(&nsenterMsg); err != nil {
		logrus.Warnf("Error decoding received nsenterMsg response: %s", err)
		return fmt.Errorf("Error decoding received nsenterMsg response: %s", err)
	}

	switch nsenterMsg.Type {

	case domain.LookupResponse:
		logrus.Debug("Received nsenterEvent lookupResponse message.")

		var p domain.FileInfo

		if payload != nil {
			err := json.Unmarshal(payload, &p)
			if err != nil {
				logrus.Error(err)
				return err
			}
		}

		e.ResMsg = &domain.NSenterMessage{
			Type:    nsenterMsg.Type,
			Payload: p,
		}
		break

	case domain.OpenFileResponse:
		logrus.Debug("Received nsenterEvent OpenResponse message.")

		var p int

		if payload != nil {
			err := json.Unmarshal(payload, &p)
			if err != nil {
				logrus.Error(err)
				return err
			}
		}

		e.ResMsg = &domain.NSenterMessage{
			Type:    nsenterMsg.Type,
			Payload: p,
		}
		break

	case domain.ReadFileResponse:
		logrus.Debug("Received nsenterEvent readResponse message.")

		var p string

		if payload != nil {
			err := json.Unmarshal(payload, &p)
			if err != nil {
				logrus.Error(err)
				return err
			}
		}

		e.ResMsg = &domain.NSenterMessage{
			Type:    nsenterMsg.Type,
			Payload: p,
		}
		break

	case domain.WriteFileResponse:
		logrus.Debug("Received nsenterEvent writeResponse message.")

		e.ResMsg = &domain.NSenterMessage{
			Type:    nsenterMsg.Type,
			Payload: "",
		}
		break

	case domain.ReadDirResponse:
		logrus.Debug("Received nsenterEvent readDirAllResponse message.")

		var p []domain.FileInfo

		if payload != nil {
			err := json.Unmarshal(payload, &p)
			if err != nil {
				logrus.Error(err)
				return err
			}
		}

		e.ResMsg = &domain.NSenterMessage{
			Type:    nsenterMsg.Type,
			Payload: p,
		}
		break

	case domain.MountSyscallResponse:
		logrus.Debug("Received nsenterEvent mountSyscallResponse message.")

		e.ResMsg = &domain.NSenterMessage{
			Type:    nsenterMsg.Type,
			Payload: "",
		}
		break

	case domain.UmountSyscallResponse:
		logrus.Debug("Received nsenterEvent umountSyscallResponse message.")

		e.ResMsg = &domain.NSenterMessage{
			Type:    nsenterMsg.Type,
			Payload: "",
		}
		break

	case domain.MountInfoResponse:
		logrus.Debug("Received nsenterEvent mountInfoResponse message.")

		var p domain.MountInfoRespPayload

		if payload != nil {
			err := json.Unmarshal(payload, &p)
			if err != nil {
				logrus.Error(err)
				return err
			}
		}

		e.ResMsg = &domain.NSenterMessage{
			Type:    nsenterMsg.Type,
			Payload: p,
		}
		break

	case domain.MountInodeResponse:
		logrus.Debug("Received nsenterEvent mountInodeResponse message.")

		var p domain.MountInodeRespPayload

		if payload != nil {
			err := json.Unmarshal(payload, &p)
			if err != nil {
				logrus.Error(err)
				return err
			}
		}

		e.ResMsg = &domain.NSenterMessage{
			Type:    nsenterMsg.Type,
			Payload: p,
		}
		break

	case domain.ChownSyscallResponse:
		logrus.Debug("Received nsenterEvent chownSyscallResponse message.")

		e.ResMsg = &domain.NSenterMessage{
			Type:    nsenterMsg.Type,
			Payload: "",
		}
		break

	case domain.SleepResponse:
		logrus.Debug("Received nsenterEvent sleepResponse message.")

		e.ResMsg = &domain.NSenterMessage{
			Type:    nsenterMsg.Type,
			Payload: "",
		}
		break

	case domain.ErrorResponse:
		logrus.Debug("Received nsenterEvent errorResponse message.")

		var p fuse.IOerror

		if payload != nil {
			err := json.Unmarshal(payload, &p)
			if err != nil {
				logrus.Error(err)
				return err
			}
		}

		e.ResMsg = &domain.NSenterMessage{
			Type:    nsenterMsg.Type,
			Payload: p,
		}
		break

	default:
		return errors.New("Received unsupported nsenterEvent message.")
	}

	return nil
}

//
// Auxiliary function to obtain the FS path associated to any given namespace.
// Theese FS paths are utilized by sysbox-runc's nsexec logic to enter the
// desired namespaces.
//
// Expected format example: "mnt:/proc/<pid>/ns/mnt"
//
func (e *NSenterEvent) namespacePaths() []string {

	var paths []string

	// Note: e.Namespace is assumed to be ordered such that if userns is present, it's
	// always first.

	for _, nstype := range *(e.Namespace) {
		path := filepath.Join(
			nstype,
			":/proc/",
			strconv.Itoa(int(e.Pid)), "/ns/",
			nstype)
		paths = append(paths, path)
	}

	return paths
}

//
// Sysbox-fs requests are generated through this method. Handlers seeking to
// access namespaced resources will call this method to invoke nsexec,
// which will enter the container namespaces that host these resources.
//
func (e *NSenterEvent) SendRequest() error {

	logrus.Debug("Executing nsenterEvent's SendRequest() method")

	// Alert the zombie reaper that nsenter is about to start
	e.reaper.nsenterStarted()

	// Create a socket pair
	parentPipe, childPipe, err := utils.NewSockPair("nsenterPipe")
	if err != nil {
		e.reaper.nsenterEnded()
		return errors.New("Error creating sysbox-fs nsenter pipe")
	}
	//defer parentPipe.Close()
	e.parentPipe = parentPipe

	// Set the SO_PASSCRED on the socket (so we can pass process credentials across it)
	socket := int(parentPipe.Fd())
	err = syscall.SetsockoptInt(socket, syscall.SOL_SOCKET, syscall.SO_PASSCRED, 1)
	if err != nil {
		return fmt.Errorf("Error setting socket options on nsenter pipe: %v", err)
	}

	// Obtain the FS path for all the namespaces to be nsenter'ed into, and
	// define the associated netlink-payload to transfer to child process.
	namespaces := e.namespacePaths()

	// Create the nsenter instruction packet
	r := nl.NewNetlinkRequest(int(libcontainer.InitMsg), 0)
	r.AddData(&libcontainer.Bytemsg{
		Type:  libcontainer.NsPathsAttr,
		Value: []byte(strings.Join(namespaces, ",")),
	})

	// Prepare exec.cmd in charge of running: "sysbox-fs nsenter".
	cmd := &exec.Cmd{
		Path:        "/proc/self/exe",
		Args:        []string{os.Args[0], "nsenter"},
		ExtraFiles:  []*os.File{childPipe},
		Env:         []string{"_LIBCONTAINER_INITPIPE=3", fmt.Sprintf("GOMAXPROCS=%s", os.Getenv("GOMAXPROCS"))},
		SysProcAttr: &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM},
		Stdin:       nil,
		Stdout:      nil,
		Stderr:      nil,
	}

	// Launch sysbox-fs' first child process.
	err = cmd.Start()
	childPipe.Close()
	if err != nil {
		logrus.Errorf("Error launching sysbox-fs first child process: %s", err)
		e.reaper.nsenterEnded()
		return errors.New("Error launching sysbox-fs first child process")
	}

	// Send the config to child process.
	if _, err := io.Copy(e.parentPipe, bytes.NewReader(r.Serialize())); err != nil {
		logrus.Warnf("Error copying payload to pipe: %s", err)
		e.reaper.nsenterReapReq()
		e.reaper.nsenterEnded()
		return errors.New("Error copying payload to pipe")
	}

	// Wait for sysbox-fs' first child process to finish.
	status, err := cmd.Process.Wait()
	if err != nil {
		logrus.Warnf("Error waiting for sysbox-fs first child process %d: %s", cmd.Process.Pid, err)
		e.reaper.nsenterReapReq()
		e.reaper.nsenterEnded()
		return err
	}
	if !status.Success() {
		logrus.Warnf("Sysbox-fs first child process error status: pid = %d", cmd.Process.Pid)
		e.reaper.nsenterReapReq()
		e.reaper.nsenterEnded()
		return errors.New("Error waiting for sysbox-fs first child process")
	}

	// Receive sysbox-fs' first-child pid.
	var pid pid
	decoder := json.NewDecoder(e.parentPipe)
	if err := decoder.Decode(&pid); err != nil {
		logrus.Warnf("Error receiving first-child pid: %s", err)
		e.reaper.nsenterEnded()
		return errors.New("Error receiving first-child pid")
	}

	firstChildProcess, err := os.FindProcess(pid.PidFirstChild)
	if err != nil {
		logrus.Warnf("Error finding first-child pid: %s", err)
		e.reaper.nsenterEnded()
		return err
	}

	// Wait for sysbox-fs' second child process to finish. Ignore the error in
	// case the child has already been reaped for any reason.
	_, _ = firstChildProcess.Wait()

	// Sysbox-fs' third child (grand-child) process remains and will enter the
	// go runtime.
	process, err := os.FindProcess(pid.Pid)
	if err != nil {
		logrus.Warnf("Error finding grand-child pid %d: %s", pid.Pid, err)
		e.reaper.nsenterEnded()
		return err
	}
	e.Process = process

	//
	// Transfer the nsenterEvent details to grand-child for processing.
	//

	// Send the pid using SCM rights, so it shows up properly inside the
	// nsexec process.
	//
	// TODO: in the future we should also send the process uid and gid
	// credentials, so that the event handler can use this info to set ownership
	// of files or mountpoints it creates on behalf of the process. This would
	// void the need to send that info in the payload as done in the
	// chown handler (i.e., it would void the need for processChownNSenter()).

	reqCred := &syscall.Ucred{
		Pid: int32(e.Pid),
	}

	credMsg := syscall.UnixCredentials(reqCred)
	if err := syscall.Sendmsg(socket, nil, credMsg, nil, 0); err != nil {
		logrus.Warnf("Error while sending process credentials to nsenter (%v).", err)
		e.reaper.nsenterReapReq()
		return err
	}

	// Transfer the rest of the payload
	data, err := json.Marshal(*(e.ReqMsg))
	if err != nil {
		logrus.Warnf("Error while encoding nsenter payload (%v).", err)
		e.reaper.nsenterReapReq()
		e.reaper.nsenterEnded()
		return err
	}
	_, err = e.parentPipe.Write(data)
	if err != nil {
		logrus.Warnf("Error while writing nsenter payload into pipeline (%v)", err)
		e.reaper.nsenterReapReq()
		e.reaper.nsenterEnded()
		return err
	}

	// Return if dealing with an asynchronous request.
	if e.async {
		return nil
	}

	// Wait for sysbox-fs' grand-child response and process it accordingly.
	ierr := e.processResponse(e.parentPipe)

	// Destroy the socket pair.
	if err := unix.Shutdown(int(parentPipe.Fd()), unix.SHUT_WR); err != nil {
		logrus.Warnf("Error shutting down sysbox-fs nsenter pipe: %s", err)
	}

	if ierr != nil {
		e.reaper.nsenterReapReq()
		e.reaper.nsenterEnded()
		return ierr
	}

	e.Process.Wait()
	e.reaper.nsenterEnded()

	return nil
}

func (e *NSenterEvent) ReceiveResponse() *domain.NSenterMessage {

	return e.ResMsg
}

// TerminateRequest serves to unwind the nsenter-event FSM after the generation
// of an asynchronous event through SendRequest(true) execution.
func (e *NSenterEvent) TerminateRequest() error {

	logrus.Debug("Executing nsenterEvent's TerminateRequest() method")

	defer e.reaper.nsenterEnded()

	if e.Process == nil {
		return nil
	}

	// Destroy the socket pair.
	if err := unix.Shutdown(int(e.parentPipe.Fd()), unix.SHUT_WR); err != nil {
		logrus.Warnf("Error shutting down sysbox-fs nsenter pipe: %s", err)
		defer e.reaper.nsenterReapReq()
		return err
	}

	// Kill ongoing request.
	if err := e.Process.Kill(); err != nil {
		defer e.reaper.nsenterReapReq()
		return err
	}

	e.Process.Wait()
	e.Process = nil

	return nil
}

///////////////////////////////////////////////////////////////////////////////
//
// nsenterEvent methods below execute within the context of container
// namespaces. In other words, they are invoked as part of "sysbox-fs nsenter"
// execution.
//
///////////////////////////////////////////////////////////////////////////////

func (e *NSenterEvent) processLookupRequest() error {

	payload := e.ReqMsg.Payload.(domain.LookupPayload)

	// Verify if the resource being looked up is reachable and obtain FileInfo
	// details.
	info, err := os.Stat(payload.Entry)
	if err != nil {
		// Send an error-message response.
		e.ResMsg = &domain.NSenterMessage{
			Type:    domain.ErrorResponse,
			Payload: &fuse.IOerror{RcvError: err},
		}

		return nil
	}

	// Allocate new FileInfo struct to return to sysbpx-fs' main instance.
	fileInfo := domain.FileInfo{
		Fname:    info.Name(),
		Fsize:    info.Size(),
		Fmode:    info.Mode(),
		FmodTime: info.ModTime(),
		FisDir:   info.IsDir(),
		Fsys:     info.Sys().(*syscall.Stat_t),
	}

	// Create a response message.
	e.ResMsg = &domain.NSenterMessage{
		Type:    domain.LookupResponse,
		Payload: fileInfo,
	}

	return nil
}

//
// Once a file has been opened with open(), no permission checking is performed
// by subsequent system calls that work with the returned file descriptor (such
// as read(), write(), fstat(), fcntl(), and mmap()).
//
func (e *NSenterEvent) processOpenFileRequest() error {

	payload := e.ReqMsg.Payload.(domain.OpenFilePayload)

	// Extract openflags from the incoming payload.
	openFlags, err := strconv.Atoi(payload.Flags)
	if err != nil {
		e.ResMsg = &domain.NSenterMessage{
			Type:    domain.ErrorResponse,
			Payload: &fuse.IOerror{RcvError: err},
		}
		return nil
	}
	// Extract openMode from the incoming payload.
	mode, err := strconv.Atoi(payload.Mode)
	if err != nil {
		e.ResMsg = &domain.NSenterMessage{
			Type:    domain.ErrorResponse,
			Payload: &fuse.IOerror{RcvError: err},
		}
		return nil
	}

	// Open the file in question. Notice that we are hardcoding the 'mode'
	// argument (third one) as this one is not relevant in a procfs; that
	// is, user cannot create files -- openflags 'O_CREAT' and 'O_TMPFILE'
	// are not expected (refer to "man open(2)" for details).
	fd, err := os.OpenFile(payload.File, openFlags, os.FileMode(mode))
	if err != nil {
		e.ResMsg = &domain.NSenterMessage{
			Type:    domain.ErrorResponse,
			Payload: &fuse.IOerror{RcvError: err},
		}
		return nil
	}
	fd.Close()

	// Create a response message.
	e.ResMsg = &domain.NSenterMessage{
		Type:    domain.OpenFileResponse,
		Payload: nil,
	}

	return nil
}

func (e *NSenterEvent) processFileReadRequest() error {

	payload := e.ReqMsg.Payload.(domain.ReadFilePayload)

	// Perform read operation and return error msg should this one fail.
	fileContent, err := ioutil.ReadFile(payload.File)
	if err != nil {
		e.ResMsg = &domain.NSenterMessage{
			Type:    domain.ErrorResponse,
			Payload: &fuse.IOerror{RcvError: err},
		}
		return nil
	}

	// Create a response message.
	e.ResMsg = &domain.NSenterMessage{
		Type:    domain.ReadFileResponse,
		Payload: strings.TrimSpace(string(fileContent)),
	}

	return nil
}

func (e *NSenterEvent) processFileWriteRequest() error {

	payload := e.ReqMsg.Payload.(domain.WriteFilePayload)

	// Perform write operation and return error msg should this one fail.
	err := ioutil.WriteFile(payload.File, []byte(payload.Content), 0644)
	if err != nil {
		e.ResMsg = &domain.NSenterMessage{
			Type:    domain.ErrorResponse,
			Payload: &fuse.IOerror{RcvError: err},
		}
		return nil
	}

	// Create a response message.
	e.ResMsg = &domain.NSenterMessage{
		Type:    domain.WriteFileResponse,
		Payload: nil,
	}

	return nil
}

func (e *NSenterEvent) processDirReadRequest() error {

	payload := e.ReqMsg.Payload.(domain.ReadDirPayload)

	// Perform readDir operation and return error msg should this one fail.
	dirContent, err := ioutil.ReadDir(payload.Dir)
	if err != nil {
		e.ResMsg = &domain.NSenterMessage{
			Type:    domain.ErrorResponse,
			Payload: &fuse.IOerror{RcvError: err},
		}
		return nil
	}

	// Create a FileInfo slice to return to sysbox-fs' main instance.
	var dirContentList []domain.FileInfo

	for _, entry := range dirContent {
		elem := domain.FileInfo{
			Fname:    entry.Name(),
			Fsize:    entry.Size(),
			Fmode:    entry.Mode(),
			FmodTime: entry.ModTime(),
			FisDir:   entry.IsDir(),
			Fsys:     entry.Sys().(*syscall.Stat_t),
		}
		dirContentList = append(dirContentList, elem)
	}

	// Create a response message.
	e.ResMsg = &domain.NSenterMessage{
		Type:    domain.ReadDirResponse,
		Payload: dirContentList,
	}

	return nil
}

func (e *NSenterEvent) processMountSyscallRequest() error {

	var (
		i   int
		err error
	)

	payload := e.ReqMsg.Payload.([]domain.MountSyscallPayload)

	// Extract payload-header from the first element
	header := payload[0].Header

	// For overlayfs mounts we adjust 'nsexec' process' personality (i.e.
	// uid/gid and capabilities) to match the one of the original process
	// performing the syscall. Our goal is mainly to avoid permission issues
	// while accessing kernel's created overlayfs components.
	if payload[0].FsType == "overlay" {

		// Create a dummy 'process' struct to represent the 'sysbox-fs nsenter' process
		// executing this logic.
		this := e.service.prs.ProcessCreate(0, 0, 0)

		// Adjust 'nsenter' process personality to match the end-user's original
		// process.
		if err := this.AdjustPersonality(
			header.Uid,
			header.Gid,
			header.Root,
			header.Cwd,
			header.Capabilities); err != nil {

			// Send an error-message response.
			e.ResMsg = &domain.NSenterMessage{
				Type:    domain.ErrorResponse,
				Payload: &fuse.IOerror{RcvError: err},
			}

			return nil
		}
	}

	process := e.service.prs.ProcessCreate(e.Pid, 0, 0)

	// Perform mount instructions.
	for i = 0; i < len(payload); i++ {

		payload[i].Source, err = process.ResolveProcSelf(payload[i].Source)
		if err != nil {
			break
		}

		payload[i].Target, err = process.ResolveProcSelf(payload[i].Target)
		if err != nil {
			break
		}

		err = unix.Mount(
			payload[i].Source,
			payload[i].Target,
			payload[i].FsType,
			uintptr(payload[i].Flags),
			payload[i].Data,
		)
		if err != nil {
			break
		}
	}

	if err != nil {
		// Unmount previously executed mount instructions (unless it's a remount).
		//
		// TODO: ideally we would revert remounts too, but to do this we need information
		// that we don't have at this stage.
		for j := i - 1; j >= 0; j-- {
			if payload[j].Flags&unix.MS_REMOUNT != unix.MS_REMOUNT {
				_ = unix.Unmount(payload[j].Target, 0)
			}
		}

		// Create error response msg.
		e.ResMsg = &domain.NSenterMessage{
			Type:    domain.ErrorResponse,
			Payload: &fuse.IOerror{RcvError: err},
		}

		return nil
	}

	// Create success response message.
	e.ResMsg = &domain.NSenterMessage{
		Type:    domain.MountSyscallResponse,
		Payload: "",
	}

	return nil
}

func (e *NSenterEvent) processUmountSyscallRequest() error {

	var (
		i   int
		err error
	)

	payload := e.ReqMsg.Payload.([]domain.UmountSyscallPayload)
	process := e.service.prs.ProcessCreate(e.Pid, 0, 0)

	// Perform umount instructions.
	for i = 0; i < len(payload); i++ {

		payload[i].Target, err = process.ResolveProcSelf(payload[i].Target)
		if err != nil {
			break
		}

		err = unix.Unmount(
			payload[i].Target,
			int(payload[i].Flags),
		)
		if err != nil {
			// Create error response msg.
			e.ResMsg = &domain.NSenterMessage{
				Type:    domain.ErrorResponse,
				Payload: &fuse.IOerror{RcvError: err},
			}

			break
		}
	}

	// TODO: If an error is found, notice that we will not revert the changes we could have
	// made thus far. In order to do that (i.e., mount again), we need information that we
	// don't have at this stage (mount-source, mount-flags, etc).
	if err != nil {
		return nil
	}

	// Create success response message.
	e.ResMsg = &domain.NSenterMessage{
		Type:    domain.UmountSyscallResponse,
		Payload: "",
	}

	return nil
}

func (e *NSenterEvent) processChownSyscallRequest() error {

	payload := e.ReqMsg.Payload.([]domain.ChownSyscallPayload)
	process := e.service.prs.ProcessCreate(e.Pid, 0, 0)

	for _, p := range payload {
		var err error

		p.Target, err = process.ResolveProcSelf(p.Target)
		if err != nil {
			break
		}

		if err = unix.Chown(p.Target, p.TargetUid, p.TargetGid); err != nil {
			e.ResMsg = &domain.NSenterMessage{
				Type:    domain.ErrorResponse,
				Payload: &fuse.IOerror{RcvError: err},
			}
			return nil
		}
	}

	e.ResMsg = &domain.NSenterMessage{
		Type:    domain.ChownSyscallResponse,
		Payload: "",
	}

	return nil
}

func (e *NSenterEvent) getProcCreds(pipe *os.File) error {

	socket := int(pipe.Fd())

	err := syscall.SetsockoptInt(socket, syscall.SOL_SOCKET, syscall.SO_PASSCRED, 1)
	if err != nil {
		return fmt.Errorf("Error setting socket options for credential passing: %v", err)
	}

	var cred syscall.Ucred
	ucred := syscall.UnixCredentials(&cred)
	buf := make([]byte, syscall.CmsgSpace(len(ucred)))

	_, rbytes, _, _, err := syscall.Recvmsg(socket, nil, buf, 0)
	if err != nil {
		return errors.New("Error decoding received process credentials.")
	}
	buf = buf[:rbytes]

	msgs, err := syscall.ParseSocketControlMessage(buf)
	if err != nil || len(msgs) != 1 {
		return errors.New("Error parsing socket control msg.")
	}

	procCred, err := syscall.ParseUnixCredentials(&msgs[0])
	if err != nil {
		return errors.New("Error parsing unix credentials.")
	}

	e.Pid = uint32(procCred.Pid)

	return nil
}

func (e *NSenterEvent) processMountInfoRequest() error {

	pid := os.Getpid()

	// Create a 'process' struct to represent the 'sysbox-fs nsenter' process
	// executing this logic.
	process := e.service.prs.ProcessCreate(uint32(pid), 0, 0)

	//
	mip, err := e.service.mts.NewMountInfoParser(
		nil,
		process,
		false,
		false,
		false)
	if err != nil {
		e.ResMsg = &domain.NSenterMessage{
			Type:    domain.ErrorResponse,
			Payload: &fuse.IOerror{RcvError: err},
		}
		return nil
	}

	// Create a MountInfo slice to return to sysbox-fs' main instance.
	mountInfoData, err := mip.ExtractMountInfo()
	if err != nil {
		e.ResMsg = &domain.NSenterMessage{
			Type:    domain.ErrorResponse,
			Payload: &fuse.IOerror{RcvError: err},
		}
		return nil
	}

	// Create a response message.
	e.ResMsg = &domain.NSenterMessage{
		Type:    domain.MountInfoResponse,
		Payload: &domain.MountInfoRespPayload{mountInfoData},
	}

	return nil
}

func (e *NSenterEvent) processMountInodeRequest() error {

	payload := e.ReqMsg.Payload.(domain.MountInodeReqPayload)

	var mpInodeList []domain.Inode

	// Iterate through the received mountpoints and extract the corresponding
	// inode.
	for _, mp := range payload.Mountpoints {
		mpInode := domain.FileInode(mp)
		mpInodeList = append(mpInodeList, mpInode)
	}

	e.ResMsg = &domain.NSenterMessage{
		Type:    domain.MountInodeResponse,
		Payload: &domain.MountInodeRespPayload{mpInodeList},
	}

	return nil
}

func (e *NSenterEvent) processSleepRequest() error {

	payload := e.ReqMsg.Payload.(domain.SleepReqPayload)

	ival, err := strconv.ParseInt(payload.Ival, 10, 64)
	if err != nil {
		e.ResMsg = &domain.NSenterMessage{
			Type:    domain.ErrorResponse,
			Payload: &fuse.IOerror{RcvError: err},
		}
		return nil
	}

	time.Sleep(time.Duration(ival) * time.Second)

	e.ResMsg = &domain.NSenterMessage{
		Type:    domain.SleepResponse,
		Payload: "",
	}

	return nil
}

// Method in charge of processing all requests generated by sysbox-fs' master
// instance.
func (e *NSenterEvent) processRequest(pipe *os.File) error {

	// Get the credentials of the process on whose behalf we are operating
	if err := e.getProcCreds(pipe); err != nil {
		return err
	}

	// Raw message payload to aid in decoding generic messages (see below
	// explanation).
	var payload json.RawMessage
	nsenterMsg := domain.NSenterMessage{
		Payload: &payload,
	}

	// Decode received msg header to help us determine the payload type.
	// Received message will be decoded in two phases. The decode instruction
	// below help us determine the message-type being received. Based on the
	// obtained type, we are able to decode the payload generated by the
	// remote-end. This second step is executed as part of a subsequent
	// unmarshal instruction (see further below).
	if err := json.NewDecoder(pipe).Decode(&nsenterMsg); err != nil {
		logrus.Warnf("Error decoding received nsenterMsg request (%v).", err)
		return errors.New("Error decoding received event request.")
	}

	switch nsenterMsg.Type {

	case domain.LookupRequest:
		var p domain.LookupPayload
		if payload != nil {
			err := json.Unmarshal(payload, &p)
			if err != nil {
				logrus.Error(err)
				return err
			}
		}

		e.ReqMsg = &domain.NSenterMessage{
			Type:    nsenterMsg.Type,
			Payload: p,
		}
		return e.processLookupRequest()

	case domain.OpenFileRequest:
		var p domain.OpenFilePayload
		if payload != nil {
			err := json.Unmarshal(payload, &p)
			if err != nil {
				logrus.Error(err)
				return err
			}
		}

		e.ReqMsg = &domain.NSenterMessage{
			Type:    nsenterMsg.Type,
			Payload: p,
		}
		return e.processOpenFileRequest()

	case domain.ReadFileRequest:
		var p domain.ReadFilePayload
		if payload != nil {
			err := json.Unmarshal(payload, &p)
			if err != nil {
				logrus.Error(err)
				return err
			}
		}

		e.ReqMsg = &domain.NSenterMessage{
			Type:    nsenterMsg.Type,
			Payload: p,
		}
		return e.processFileReadRequest()

	case domain.WriteFileRequest:
		var p domain.WriteFilePayload
		if payload != nil {
			err := json.Unmarshal(payload, &p)
			if err != nil {
				logrus.Error(err)
				return err
			}
		}

		e.ReqMsg = &domain.NSenterMessage{
			Type:    nsenterMsg.Type,
			Payload: p,
		}
		return e.processFileWriteRequest()

	case domain.ReadDirRequest:
		var p domain.ReadDirPayload
		if payload != nil {
			err := json.Unmarshal(payload, &p)
			if err != nil {
				logrus.Error(err)
				return err
			}
		}

		e.ReqMsg = &domain.NSenterMessage{
			Type:    nsenterMsg.Type,
			Payload: p,
		}
		return e.processDirReadRequest()

	// case domain.SetAttrRequest:
	// 	var p domain.SetAttrPayload
	// 	if payload != nil {
	// 		err := json.Unmarshal(payload, &p)
	// 		if err != nil {
	// 			logrus.Error(err)
	// 			return err
	// 		}
	// 	}

	// 	e.ReqMsg = &domain.NSenterMessage{
	// 		Type:    nsenterMsg.Type,
	// 		Payload: p,
	// 	}
	// 	return e.processSetAttrRequest()

	case domain.MountSyscallRequest:
		var p []domain.MountSyscallPayload
		if payload != nil {
			err := json.Unmarshal(payload, &p)
			if err != nil {
				logrus.Error(err)
				return err
			}
		}

		e.ReqMsg = &domain.NSenterMessage{
			Type:    nsenterMsg.Type,
			Payload: p,
		}

		return e.processMountSyscallRequest()

	case domain.UmountSyscallRequest:
		var p []domain.UmountSyscallPayload
		if payload != nil {
			err := json.Unmarshal(payload, &p)
			if err != nil {
				logrus.Error(err)
				return err
			}
		}

		e.ReqMsg = &domain.NSenterMessage{
			Type:    nsenterMsg.Type,
			Payload: p,
		}

		return e.processUmountSyscallRequest()

	case domain.MountInfoRequest:
		e.ReqMsg = &domain.NSenterMessage{
			Type: nsenterMsg.Type,
		}

		return e.processMountInfoRequest()

	case domain.MountInodeRequest:
		var p domain.MountInodeReqPayload
		if payload != nil {
			err := json.Unmarshal(payload, &p)
			if err != nil {
				logrus.Error(err)
				return err
			}
		}

		e.ReqMsg = &domain.NSenterMessage{
			Type:    nsenterMsg.Type,
			Payload: p,
		}

		return e.processMountInodeRequest()

	case domain.ChownSyscallRequest:
		var p []domain.ChownSyscallPayload
		if payload != nil {
			err := json.Unmarshal(payload, &p)
			if err != nil {
				logrus.Error(err)
				return err
			}
		}

		e.ReqMsg = &domain.NSenterMessage{
			Type:    nsenterMsg.Type,
			Payload: p,
		}

		return e.processChownSyscallRequest()

	case domain.SleepRequest:
		var p domain.SleepReqPayload
		if payload != nil {
			err := json.Unmarshal(payload, &p)
			if err != nil {
				logrus.Error(err)
				return err
			}
		}

		e.ReqMsg = &domain.NSenterMessage{
			Type:    nsenterMsg.Type,
			Payload: p,
		}

		return e.processSleepRequest()

	default:
		e.ResMsg = &domain.NSenterMessage{
			Type:    domain.ErrorResponse,
			Payload: "Unsupported request",
		}
	}

	return nil
}

//
// Sysbox-fs' post-nsexec initialization function. To be executed within the
// context of one (or more) container namespaces.
//
func Init() (err error) {

	var (
		pipefd      int
		envInitPipe = os.Getenv("_LIBCONTAINER_INITPIPE")
	)

	// Get the INITPIPE.
	pipefd, err = strconv.Atoi(envInitPipe)
	if err != nil {
		return fmt.Errorf("Unable to convert _LIBCONTAINER_INITPIPE=%s to int: %s",
			envInitPipe, err)
	}

	var pipe = os.NewFile(uintptr(pipefd), "pipe")
	defer pipe.Close()

	// Clear the current process's environment to clean any libcontainer
	// specific env vars.
	os.Clearenv()

	// Setup nsenterService and its dependencies.
	var nsenterSvc = NewNSenterService()
	var processSvc = process.NewProcessService()
	var mountSvc = mount.NewMountService()
	nsenterSvc.Setup(processSvc, mountSvc)
	mountSvc.Setup(nil, nil, processSvc, nsenterSvc)

	var event = NSenterEvent{service: nsenterSvc.(*nsenterService)}

	// Process incoming request.
	err = event.processRequest(pipe)
	if err != nil {
		event.ResMsg = &domain.NSenterMessage{
			Type:    domain.ErrorResponse,
			Payload: &fuse.IOerror{RcvError: err},
		}
	}

	// Encode / push response back to sysbox-main.
	data, err := json.Marshal(*(event.ResMsg))
	if err != nil {
		return err
	}
	_, err = pipe.Write(data)
	if err != nil {
		return err
	}

	return nil
}
