//go:build !amd64

package platform

var CpuFeatures CpuFeatureFlags = &cpuFeatureFlags{}

// cpuFeatureFlags implements CpuFeatureFlags for unsupported platforms
type cpuFeatureFlags struct{}

// Has implements the same method on the CpuFeatureFlags interface
func (c *cpuFeatureFlags) Has(cpuFeature CpuFeature) bool { return false }

// HasExtra implements the same method on the CpuFeatureFlags interface
func (c *cpuFeatureFlags) HasExtra(cpuFeature CpuFeature) bool { return false }
