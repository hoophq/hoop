package runtime

import (
	"os"
	"os/exec"
	"strings"
	"syscall"
)

func Kill(pid int, signum syscall.Signal) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Signal(signum)
}

func runCommand(command string, arg ...string) *string {
	if os.Getenv("HOSTNAME") != "" {
		v := os.Getenv("HOSTNAME")
		return &v
	}
	resp, _ := exec.Command(command, arg...).Output()
	if resp == nil {
		return nil
	}
	v := strings.TrimSuffix(string(resp), "\n")
	v = strings.TrimSpace(v)
	return &v
}

func readFile(path string) *string {
	machineID, _ := os.ReadFile(path)
	if machineID == nil {
		return nil
	}
	v := strings.TrimSuffix(string(machineID), "\n")
	return &v
}

func OS() map[string]string {
	return map[string]string{
		"hostname":       String(runCommand("hostname")),
		"kernel_version": String(runCommand("uname", "-a")),
		"machine_id":     String(readFile("/sys/class/dmi/id/product_uuid")),
	}
}

func String(val interface{}) string {
	v, _ := val.(*string)
	if v == nil {
		return ""
	}
	return *v
}
