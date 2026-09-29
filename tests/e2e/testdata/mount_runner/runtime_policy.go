package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"unsafe"

	"github.com/EpicBlackWolfZ/microfat/tests/e2e/testdata/mountfixture"
	"golang.org/x/sys/unix"
)

const (
	seccompArchOffset    = 4
	seccompCommandOffset = 24
	policyPollMillis     = 100
	mandatorySeals       = unix.F_SEAL_WRITE | unix.F_SEAL_GROW | unix.F_SEAL_SHRINK | unix.F_SEAL_SEAL
	sealFilterSkip       = 3
	descriptorBytes      = 4
)

type notificationData struct {
	Number int32
	Arch   uint32
	IP     uint64
	Args   [6]uint64
}
type notification struct {
	ID    uint64
	PID   uint32
	Flags uint32
	Data  notificationData
}
type notificationResponse struct {
	ID    uint64
	Value int64
	Error int32
	Flags uint32
}
type policyResult struct {
	events []mountfixture.PolicyEvent
	err    error
}

func policyNeedsListener(policy string) bool {
	return policy == "exec-first" || policy == "exec-all" || policy == "fd-pressure" || policy == "observe"
}

func runtimeFilter(policy string) []unix.SockFilter {
	filters := []unix.SockFilter{
		{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: seccompArchOffset},
		{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, Jt: 1, K: runtimeAuditArch()},
		{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_KILL_PROCESS},
		{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS},
	}
	action := unix.SECCOMP_RET_ERRNO | uint32(unix.EPERM)
	syscalls := []uint32{unix.SYS_MEMFD_CREATE}
	switch policy {
	case "create-enosys":
		action = unix.SECCOMP_RET_ERRNO | uint32(unix.ENOSYS)
	case "seal":
		filters = append(filters,
			unix.SockFilter{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, Jf: sealFilterSkip, K: unix.SYS_FCNTL},
			unix.SockFilter{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: seccompCommandOffset},
			unix.SockFilter{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, Jf: 1, K: unix.F_ADD_SEALS},
			unix.SockFilter{Code: unix.BPF_RET | unix.BPF_K, K: action})
		syscalls = nil
	case "exec-first", "exec-all", "observe":
		syscalls, action = []uint32{unix.SYS_EXECVE, unix.SYS_EXECVEAT}, unix.SECCOMP_RET_USER_NOTIF
	case "fd-pressure":
		action = unix.SECCOMP_RET_USER_NOTIF
	}
	for _, number := range syscalls {
		filters = append(filters,
			unix.SockFilter{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, Jf: 1, K: number},
			unix.SockFilter{Code: unix.BPF_RET | unix.BPF_K, K: action})
	}
	return append(filters, unix.SockFilter{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ALLOW})
}

func installRuntimePolicy(policy string, socket int) error {
	if policy == "" {
		return nil
	}
	filter := runtimeFilter(policy)
	// #nosec G115 -- bounded static filter contains fewer than 32 instructions.
	program := unix.SockFprog{Len: uint16(len(filter)), Filter: &filter[0]}
	flags := uintptr(0)
	if policyNeedsListener(policy) {
		if err := checkNotificationABI(); err != nil {
			return err
		}
		flags = unix.SECCOMP_FILTER_FLAG_NEW_LISTENER
	}
	// #nosec G103 -- Linux UAPI takes the address of this live, bounded SockFprog.
	fd, _, errno := unix.Syscall(unix.SYS_SECCOMP, unix.SECCOMP_SET_MODE_FILTER, flags, uintptr(unsafe.Pointer(&program)))
	if errno != 0 {
		return errno
	}
	if !policyNeedsListener(policy) {
		return nil
	}
	if err := unix.Sendmsg(socket, []byte{1}, unix.UnixRights(int(fd)), nil, 0); err != nil {
		_ = unix.Close(int(fd))
		return err
	}
	return unix.Close(int(fd))
}

func checkNotificationABI() error {
	var sizes struct{ Notice, Response, Data uint16 }
	// #nosec G103 -- documented kernel size query writes exactly this three-uint16 UAPI structure.
	_, _, errno := unix.Syscall(unix.SYS_SECCOMP, unix.SECCOMP_GET_NOTIF_SIZES, 0, uintptr(unsafe.Pointer(&sizes)))
	if errno != 0 {
		return errno
	}
	if uintptr(sizes.Notice) != unsafe.Sizeof(notification{}) ||
		uintptr(sizes.Response) != unsafe.Sizeof(notificationResponse{}) || uintptr(sizes.Data) != unsafe.Sizeof(notificationData{}) {
		return errors.New("unsupported seccomp notification ABI")
	}
	return nil
}

func notificationValid(fd int, id *uint64) error {
	// #nosec G103 -- kernel notification identity UAPI, pointer to a live uint64.
	return policyIOCTL(fd, unix.SECCOMP_IOCTL_NOTIF_ID_VALID, unsafe.Pointer(id))
}

func sendNotification(fd int, response *notificationResponse) error {
	// #nosec G103 -- kernel notification response UAPI, pointer to a live response structure.
	return policyIOCTL(fd, unix.SECCOMP_IOCTL_NOTIF_SEND, unsafe.Pointer(response))
}

func pollPolicy(ctx context.Context, fd int) (bool, error) {
	if fd < 0 || fd > math.MaxInt32 {
		return false, errors.New("invalid policy descriptor")
	}
	for ctx.Err() == nil {
		poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		_, err := unix.Poll(poll, policyPollMillis)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return false, err
		}
		if poll[0].Revents&(unix.POLLERR|unix.POLLNVAL) != 0 {
			return false, errors.New("policy listener failed")
		}
		if poll[0].Revents&unix.POLLIN != 0 {
			return true, nil
		}
		if poll[0].Revents&unix.POLLHUP != 0 {
			return false, nil
		}
	}
	return false, ctx.Err()
}

func receiveRuntimeListener(ctx context.Context, socket int) (int, error) {
	ok, err := pollPolicy(ctx, socket)
	if err != nil || !ok {
		return -1, errors.Join(err, errors.New("missing policy listener"))
	}
	data, control := make([]byte, 1), make([]byte, unix.CmsgSpace(descriptorBytes))
	n, oob, flags, _, err := unix.Recvmsg(socket, data, control, unix.MSG_CMSG_CLOEXEC)
	if err != nil {
		return -1, err
	}
	messages, err := unix.ParseSocketControlMessage(control[:oob])
	if err != nil {
		return -1, err
	}
	var descriptors []int
	for _, message := range messages {
		fds, err := unix.ParseUnixRights(&message)
		if err != nil {
			return -1, err
		}
		descriptors = append(descriptors, fds...)
	}
	if len(descriptors) != 1 || n != 1 || data[0] != 1 || flags&(unix.MSG_TRUNC|unix.MSG_CTRUNC) != 0 {
		for _, fd := range descriptors {
			_ = unix.Close(fd)
		}
		return -1, errors.New("invalid transfer: expected one policy listener")
	}
	return descriptors[0], nil
}

func policyIOCTL(fd int, request uintptr, pointer unsafe.Pointer) error {
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), request, uintptr(pointer))
	if errno != 0 {
		return errno
	}
	return nil
}

func superviseRuntime(ctx context.Context, policy string, socket, pid int) policyResult {
	var result policyResult
	completed, cancelled := 0, 0
	const cancellationLimit = 128
	if !policyNeedsListener(policy) {
		return result
	}
	fd, err := receiveRuntimeListener(ctx, socket)
	if err != nil {
		result.err = err
		return result
	}
	defer func() { _ = unix.Close(fd) }()
	for {
		ready, err := pollPolicy(ctx, fd)
		if err != nil || !ready {
			result.err = err
			return result
		}
		var notice notification
		// #nosec G103 -- documented Linux notification UAPI, live struct owned by this supervisor.
		if err := policyIOCTL(fd, unix.SECCOMP_IOCTL_NOTIF_RECV, unsafe.Pointer(&notice)); err != nil {
			if errors.Is(err, unix.EINTR) || errors.Is(err, unix.ENOENT) {
				continue
			}
			result.err = err
			return result
		}
		// #nosec G103 -- kernel notification ID validation, pointer to a live uint64.
		if err := policyIOCTL(fd, unix.SECCOMP_IOCTL_NOTIF_ID_VALID, unsafe.Pointer(&notice.ID)); err != nil {
			if errors.Is(err, unix.ENOENT) {
				continue
			}
			result.err = err
			return result
		}
		event, response, err := decideRuntimeNotification(policy, notice, completed, pid)
		validErr := notificationValid(fd, &notice.ID)
		if errors.Is(validErr, unix.ENOENT) {
			err = validErr
		} else if err == nil {
			err = validErr
		}
		if err == nil {
			err = sendNotification(fd, &response)
		}
		// Go's asynchronous preemption can cancel a notification. Only a
		// kernel-confirmed invalid ID may be retried; no exec has been authorized.
		if errors.Is(err, unix.ENOENT) && errors.Is(notificationValid(fd, &notice.ID), unix.ENOENT) {
			cancelled++
			event.Decision = "cancelled"
			result.events = append(result.events, event)
			if cancelled > cancellationLimit {
				result.err = errors.New("too many cancelled notifications")
				return result
			}
			continue
		}
		result.events = append(result.events, event)
		if err != nil {
			result.err = err
			return result
		}
		completed++
	}
}

func decideRuntimeNotification(policy string, notice notification, ordinal, pid int) (
	mountfixture.PolicyEvent, notificationResponse, error,
) {
	event := mountfixture.PolicyEvent{PID: notice.PID, Syscall: notice.Data.Number, Decision: "continue"}
	response := notificationResponse{ID: notice.ID, Flags: unix.SECCOMP_USER_NOTIF_FLAG_CONTINUE}
	if notice.Data.Arch != runtimeAuditArch() {
		return event, response, errors.New("notification architecture mismatch")
	}
	if policy == "fd-pressure" {
		if notice.Data.Number != unix.SYS_MEMFD_CREATE || ordinal != 0 {
			return event, response, errors.New("unexpected FD pressure notification")
		}
		var limit unix.Rlimit
		if err := unix.Prlimit(pid, unix.RLIMIT_NOFILE, nil, &limit); err != nil {
			return event, response, err
		}
		limit.Cur = 0
		event.Decision = "continue-with-nofile-zero"
		return event, response, unix.Prlimit(pid, unix.RLIMIT_NOFILE, &limit, nil)
	}
	if notice.Data.Number != unix.SYS_EXECVE && notice.Data.Number != unix.SYS_EXECVEAT {
		return event, response, errors.New("unexpected exec notification")
	}
	if ordinal == 0 {
		event.Decision = "bootstrap"
		return event, response, nil
	}
	if err := inspectRuntimeTarget(notice, &event, policy == "observe"); err != nil {
		return event, response, err
	}
	if policy == "exec-all" || (policy == "exec-first" && ordinal == 1) {
		event.Decision = "deny-EPERM"
		response.Flags, response.Error = 0, -int32(unix.EPERM)
	}
	return event, response, nil
}

func inspectRuntimeTarget(notice notification, event *mountfixture.PolicyEvent, inspectSeals bool) error {
	address := notice.Data.Args[0]
	if notice.Data.Number == unix.SYS_EXECVEAT {
		address = notice.Data.Args[1]
	}
	if address > math.MaxInt64 {
		return errors.New("invalid notification path pointer")
	}
	file, err := os.Open(fmt.Sprintf("/proc/%d/mem", notice.PID))
	if err != nil {
		return err
	}
	buffer := make([]byte, startupLimit)
	n, readErr := file.ReadAt(buffer, int64(address))
	closeErr := file.Close()
	end := bytes.IndexByte(buffer[:n], 0)
	if end < 0 || closeErr != nil {
		return errors.Join(readErr, closeErr, errors.New("unbounded exec pathname"))
	}
	path := string(buffer[:end])
	fdText, ok := strings.CutPrefix(path, "/proc/self/fd/")
	if !ok {
		return errors.New("payload exec did not use a descriptor")
	}
	fd, err := strconv.Atoi(fdText)
	if err != nil || fd < 0 {
		return errors.New("invalid payload descriptor")
	}
	path = runtimeDescriptorPath(notice.PID, fd)
	event.Target, err = os.Readlink(path)
	if err != nil {
		return err
	}
	flags := os.O_RDONLY
	memfd := strings.Contains(event.Target, "memfd:microfat_payload")
	if inspectSeals && memfd {
		flags = os.O_RDWR
	}
	// #nosec G304 -- numeric descriptor under the notified fixture task's procfs directory.
	file, err = os.OpenFile(path, flags, 0)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	before, err := hashRuntimeFile(file)
	if err != nil {
		return err
	}
	event.Digest = before
	if !memfd {
		return nil
	}
	event.Seals, err = unix.FcntlInt(file.Fd(), unix.F_GET_SEALS, 0)
	if err != nil || event.Seals&mandatorySeals != mandatorySeals {
		return errors.Join(err, errors.New("missing mandatory seals"))
	}
	if !inspectSeals {
		return nil
	}
	stat, err := file.Stat()
	if err != nil {
		return err
	}
	_, writeErr := file.WriteAt([]byte{0}, 0)
	shrinkErr, growErr := file.Truncate(stat.Size()-1), file.Truncate(stat.Size()+1)
	_, sealErr := unix.FcntlInt(file.Fd(), unix.F_ADD_SEALS, unix.F_SEAL_FUTURE_WRITE)
	after, hashErr := hashRuntimeFile(file)
	event.Immutable = errors.Is(writeErr, unix.EPERM) && errors.Is(shrinkErr, unix.EPERM) &&
		errors.Is(growErr, unix.EPERM) && errors.Is(sealErr, unix.EPERM) && before == after
	if !event.Immutable || hashErr != nil {
		return errors.Join(hashErr, errors.New("memfd immutability control failed"))
	}
	return nil
}

func hashRuntimeFile(file *os.File) (string, error) {
	hash := sha256.New()
	if _, err := io.Copy(hash, io.NewSectionReader(file, 0, math.MaxInt64)); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}
