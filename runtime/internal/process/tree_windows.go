package process

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type processTree struct{ job windows.Handle }

func startTree(cmd *exec.Cmd) (*processTree, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("create job object: %w", err)
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		return nil, errors.Join(err, windows.CloseHandle(job))
	}
	// Suspended creation prevents the plugin from spawning children before it
	// belongs to the job. Fail closed if assignment or resume is unavailable.
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED, HideWindow: true}
	if err := cmd.Start(); err != nil {
		return nil, errors.Join(err, windows.CloseHandle(job))
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_SUSPEND_RESUME, false, uint32(cmd.Process.Pid))
	if err != nil {
		return nil, errors.Join(err, cmd.Process.Kill(), cmd.Wait(), windows.CloseHandle(job))
	}
	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		return nil, errors.Join(err, cmd.Process.Kill(), cmd.Wait(), windows.CloseHandle(process), windows.CloseHandle(job))
	}
	resume := windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess")
	if err := resume.Find(); err != nil {
		return nil, errors.Join(err, windows.TerminateJobObject(job, 1), cmd.Wait(), windows.CloseHandle(process), windows.CloseHandle(job))
	}
	status, _, _ := resume.Call(uintptr(process))
	if status != 0 {
		return nil, errors.Join(fmt.Errorf("resume job process: %w", windows.NTStatus(status)), windows.TerminateJobObject(job, 1), cmd.Wait(), windows.CloseHandle(process), windows.CloseHandle(job))
	}
	if err := windows.CloseHandle(process); err != nil {
		return nil, errors.Join(err, windows.TerminateJobObject(job, 1), cmd.Wait(), windows.CloseHandle(job))
	}
	return &processTree{job: job}, nil
}

// Windows has no general SIGTERM equivalent; pipe closure provides the grace
// interval, after which the job is forcibly terminated.
func (p *processTree) terminate() error { return nil }
func (p *processTree) kill() error      { return windows.TerminateJobObject(p.job, 0xC0D15001) }
func (p *processTree) close() error {
	// TerminateJobObject is asynchronous. Waiting only for the leader leaves
	// descendants holding the activation executable and inherited handles.
	deadline := time.Now().Add(30 * time.Second)
	for {
		var accounting struct {
			TotalUserTime, TotalKernelTime, ThisPeriodTotalUserTime, ThisPeriodTotalKernelTime int64
			TotalPageFaultCount, TotalProcesses, ActiveProcesses, TotalTerminatedProcesses     uint32
		}
		if err := windows.QueryInformationJobObject(p.job, windows.JobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(&accounting)), uint32(unsafe.Sizeof(accounting)), nil); err != nil {
			return errors.Join(fmt.Errorf("query job drain: %w", err), windows.CloseHandle(p.job))
		}
		if accounting.ActiveProcesses == 0 {
			return windows.CloseHandle(p.job)
		}
		if !time.Now().Before(deadline) {
			return errors.Join(errors.New("windows job still has active processes after termination"), windows.CloseHandle(p.job))
		}
		time.Sleep(time.Millisecond)
	}
}
func (p *processTree) expectedExit(err *exec.ExitError) bool {
	return uint32(err.ExitCode()) == 0xC0D15001
}
