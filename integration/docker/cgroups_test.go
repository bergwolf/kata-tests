// Copyright (c) 2019 Intel Corporation
//
// SPDX-License-Identifier: Apache-2.0

package docker

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

type cgroupType string

const (
	cgroupCPU    cgroupType = "cpu"
	cgroupCpuset            = "cpuset"
)

const (
	sysCgroupPath     = "/sys/fs/cgroup/"
	dockerCgroupName  = "docker"
	sysCPUSharesFile  = "cpu.shares"
	sysCPUQuotaFile   = "cpu.cfs_quota_us"
	sysCPUPeriodFile  = "cpu.cfs_period_us"
	sysCpusetCpusFile = "cpuset.cpus"
)

func containerID(name string) (string, error) {
	stdout, stderr, exitCode := dockerInspect("--format", "{{.Id}}", name)
	if exitCode != 0 {
		return "", fmt.Errorf("Could not get container ID: %v", stderr)
	}
	return strings.Trim(stdout, "\n\t "), nil
}

func containerCgroupParent(name string) (string, error) {
	stdout, stderr, exitCode := dockerInspect("--format", "{{.HostConfig.CgroupParent}}", name)
	if exitCode != 0 {
		return "", fmt.Errorf("Could not get container cgroup parent: %v", stderr)
	}
	return strings.Trim(stdout, "\n\t "), nil
}

func containerCgroupPath(name string, t cgroupType) (string, error) {
	parentCgroup := dockerCgroupName
	if path, err := containerCgroupParent(name); err != nil && path != "" {
		parentCgroup = path
	}

	if id, err := containerID(name); err == nil && id != "" {
		return filepath.Join(sysCgroupPath, string(t), parentCgroup, id), nil
	}

	return "", fmt.Errorf("Could not get container cgroup path")
}

func addProcessToCgroup(pid int, cgroupPath string) error {
	return ioutil.WriteFile(filepath.Join(cgroupPath, "cgroup.procs"),
		[]byte(fmt.Sprintf("%v", pid)), os.FileMode(0775))
}

var _ = Describe("Checking CPU cgroups in the host", func() {
	var (
		args             []string
		id               string
		cpuCgroupPath    string
		cpusetCgroupPath string
		err              error
		exitCode         int
		expectedShares   string
		expectedQuota    string
		expectedPeriod   string
		expectedCpuset   string
	)

	BeforeEach(func() {
		id = randomDockerName()
		args = []string{"--cpus=1", "--cpu-shares=800", "--cpuset-cpus=0", "-dt", "--name", id, Image, "sh"}
	})

	AfterEach(func() {
		Expect(ExistDockerContainer(id)).NotTo(BeTrue())
	})

	Describe("checking whether cgroups can be deleted", func() {
		Context("with a running process", func() {
			It("should be deleted", func() {
				if os.Getuid() != 0 {
					Skip("only root user can modify cgroups")
				}

				_, _, exitCode = dockerRun(args...)
				Expect(exitCode).To(BeZero())

				// check that cpu cgroups exist
				cpuCgroupPath, err = containerCgroupPath(id, cgroupCPU)
				Expect(err).ToNot(HaveOccurred())
				Expect(cpuCgroupPath).Should(BeADirectory())

				cpusetCgroupPath, err = containerCgroupPath(id, cgroupCpuset)
				Expect(err).ToNot(HaveOccurred())
				Expect(cpusetCgroupPath).Should(BeADirectory())

				// Add current process to cgroups
				err = addProcessToCgroup(os.Getpid(), cpuCgroupPath)
				Expect(err).ToNot(HaveOccurred())

				err = addProcessToCgroup(os.Getpid(), cpusetCgroupPath)
				Expect(err).ToNot(HaveOccurred())

				// remove container
				Expect(RemoveDockerContainer(id)).To(BeTrue())

				// cgroups shouldn't exist
				Expect(cpuCgroupPath).ShouldNot(BeADirectory())
				Expect(cpusetCgroupPath).ShouldNot(BeADirectory())
			})
		})
	})

	Describe("checking whether cgroups are updated", func() {
		Context("updating container cpu and cpuset cgroup", func() {
			It("should be updated", func() {
				_, _, exitCode = dockerRun(args...)
				Expect(exitCode).To(BeZero())

				expectedShares = "738"
				expectedQuota = "250000"
				expectedPeriod = "100000"
				expectedCpuset = "1"

				if runtime.GOARCH == "ppc64le" {
					expectedCpuset = "8"
				}
				_, _, exitCode = dockerUpdate("--cpus=2.5", "--cpu-shares", expectedShares, "--cpuset-cpus", expectedCpuset, id)
				Expect(exitCode).To(BeZero())

				cpuCgroupPath, err = containerCgroupPath(id, cgroupCPU)
				Expect(err).ToNot(HaveOccurred())

				cpusetCgroupPath, err = containerCgroupPath(id, cgroupCpuset)
				Expect(err).ToNot(HaveOccurred())

				for r, v := range map[string]string{
					filepath.Join(cpuCgroupPath, sysCPUQuotaFile):      expectedQuota,
					filepath.Join(cpuCgroupPath, sysCPUPeriodFile):     expectedPeriod,
					filepath.Join(cpuCgroupPath, sysCPUSharesFile):     expectedShares,
					filepath.Join(cpusetCgroupPath, sysCpusetCpusFile): expectedCpuset,
				} {
					c, err := ioutil.ReadFile(r)
					Expect(err).ToNot(HaveOccurred())
					Expect(strings.Trim(string(c), "\n\t ")).To(Equal(v))
				}

				Expect(RemoveDockerContainer(id)).To(BeTrue())
			})
		})
	})
})
