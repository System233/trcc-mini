// SPDX-License-Identifier: GPL-3.0-or-later
// Read-only multi-device discovery and telemetry inspection.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"time"
)

type sensorListing struct {
	Devices []hardwareDevice `json:"devices"`
	Metrics []sensorReading  `json:"metrics"`
}
type sensorReading struct {
	Name  string   `json:"name"`
	Unit  string   `json:"unit"`
	Value *float64 `json:"value"`
}

func enumerateSensorReadings() (sensorListing, error) {
	devices, e := discoverHardware()
	if e != nil {
		return sensorListing{}, e
	}
	result := sensorListing{Devices: devices}
	snapshot := metrics{}
	var cpu cpuLoadSampler
	var cpuDetails cpuDetailSampler
	_ = cpu.Sample()             // first read primes cumulative CPU load counters
	cpuDetails.Sample(&snapshot) // first per-core sample primes counters
	// Two immediate cumulative CPU reads usually report 0% by accident.
	time.Sleep(250 * time.Millisecond)
	snapshot.Load = cpu.Sample()
	cpuDetails.Sample(&snapshot)
	if sensor, e := openCPUSensor(); e == nil {
		if value, e := sensor.ReadTemp("tctl"); e == nil {
			snapshot.Temp, snapshot.TempValid = value, true
		}
		_, _, _ = sensor.ReadPower() // prime energy counter
		time.Sleep(250 * time.Millisecond)
		if value, valid, e := sensor.ReadPower(); e == nil && valid {
			snapshot.CPUPower, snapshot.CPUPowerValid = value, true
		}
		populatePlatformCPUMetrics(&snapshot, sensor)
		sensor.Close()
	} else {
		populatePlatformCPUMetrics(&snapshot, nil)
	}
	populateCPUIndexed(&snapshot)
	if gpu, e := openGPUSensor(); e == nil {
		if inventory, ok := gpu.(interface{ Devices() []hardwareDevice }); ok {
			providers := inventory.Devices()
			for i := range result.Devices {
				if result.Devices[i].Kind != "gpu" {
					continue
				}
				for _, provided := range providers {
					if provided.Index == result.Devices[i].Index {
						result.Devices[i].Driver = provided.Driver
						break
					}
				}
			}
		}
		snapshot.GPUTemp, snapshot.GPUTempValid, snapshot.GPULoad, snapshot.GPULoadValid = gpu.Read()
		snapshot.GPUPower, snapshot.GPUPowerValid = gpu.ReadPower()
		if used, total, load, ok := gpu.ReadMemory(); ok {
			snapshot.setExtra("gpu.memory.used", used)
			snapshot.setExtra("gpu.memory.total", total)
			snapshot.setExtra("gpu.memory.load", load)
		}
		if indexed, ok := gpu.(indexedGPU); ok {
			indexed.SampleIndexed(&snapshot)
		}
		gpu.Close()
	}
	if used, total, load, ok := readSystemMemory(); ok {
		snapshot.setExtra("memory.used", used)
		snapshot.setExtra("memory.total", total)
		snapshot.setExtra("memory.load", load)
	}
	names := map[string]bool{}
	for _, dev := range devices {
		catalog := cpuMetricUnits
		if dev.Kind == "gpu" {
			catalog = gpuMetricUnits
		}
		for suffix := range catalog {
			names[indexedMetricName(dev.Kind, dev.Index, suffix)] = true
		}
	}
	for _, name := range listPlatformCPUDetailMetrics() {
		names[name] = true
	}
	for name := range snapshot.Extras {
		if metricNativeUnit(name) != "" {
			names[name] = true
		}
	}
	for key := range builtinUnits {
		names[key] = true
	}
	for _, name := range []string{
		"cpu.temperature.max", "cpu.clock", "cpu.clock.max",
		"gpu.temperature.hotspot", "gpu.temperature.memory", "gpu.power.limit",
		"gpu.clock.core", "gpu.clock.memory", "gpu.fan.load", "gpu.pcie.tx", "gpu.pcie.rx",
	} {
		names[name] = true
	}
	keys := make([]string, 0, len(names))
	for key := range names {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, name := range keys {
		value, valid := readMetric(snapshot, name)
		var ptr *float64
		if valid && value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0) {
			v := value
			ptr = &v
		}
		result.Metrics = append(result.Metrics, sensorReading{Name: name, Unit: metricNativeUnit(name), Value: ptr})
	}
	return result, nil
}

func printSensorList(out io.Writer, jsonOutput bool) error {
	result, err := enumerateSensorReadings()
	if err != nil {
		return err
	}
	if jsonOutput {
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}
	fmt.Fprintln(out, "Hardware (PCI topology order; indices may change after hardware replacement):")
	if len(result.Devices) == 0 {
		fmt.Fprintln(out, "  No hardware metadata exposed by this OS")
	}
	for _, dev := range result.Devices {
		fmt.Fprintf(out, "  %s%d  %s  [%s]  bus=%s  telemetry=%s\n",
			dev.Kind, dev.Index, dev.Name, dev.Vendor, dev.Bus, dev.Driver)
	}
	fmt.Fprintln(out, "Metrics (N/A means no valid sensor data, not 0):")
	for _, metric := range result.Metrics {
		if metric.Value == nil {
			fmt.Fprintf(out, "  %-32s N/A\n", metric.Name)
		} else {
			fmt.Fprintf(out, "  %-32s %.2f %s\n", metric.Name, *metric.Value, metric.Unit)
		}
	}
	fmt.Fprintln(out, "Aggregate cpu./gpu. metrics ignore missing values; PCI identities prevent automatic substitution.")
	return nil
}

func sensorListCommand(args []string) error {
	if len(args) == 0 {
		return printSensorList(os.Stdout, false)
	}
	if len(args) == 1 && args[0] == "--json" {
		return printSensorList(os.Stdout, true)
	}
	return fmt.Errorf("usage: trcc-mini sensors list [--json]")
}
