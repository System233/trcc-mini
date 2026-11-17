# TRCC Mini — Windows HID digital display CLI

A minimal, **single executable**, no-Python, no-.NET replacement for the
Thermalright TRCC digital/segment HID device `0416:8001`.

Source protocol and digital display mappings are ported from the community
[thermalright-trcc-linux](https://github.com/Lexonight1/thermalright-trcc-linux)
GPL-3.0 implementation; this does **not** install or run that larger package.

## Build

Windows Go 1.22 or newer, no packages outside the Go standard library:

```powershell
go build -trimpath -buildvcs=false -ldflags="-s -w" -o trcc-mini.exe .
```

Only `trcc-mini.exe` is required at runtime. The officially signed
`AMDFamily17.bin` module is embedded and pinned by SHA-256.

## Commands

```powershell
.\trcc-mini.exe detect                             # read-only USB enumeration
.\trcc-mini.exe sensors                            # read-only sensor comparison; no USB I/O
.\trcc-mini.exe probe                              # handshake ONCE, save PM cache
.\trcc-mini.exe run -pm 16 -link load              # PA120 example: CPU% linked RGB
.\trcc-mini.exe run -pm 16 -link temp              # CPU °C linked RGB
.\trcc-mini.exe run -pm 16 -gpu -link gpu-temp     # optional RTX 4070 GPU
.\trcc-mini.exe run -gpu -power rotate              # PA120: alternate temperature and watts
.\trcc-mini.exe run -gpu -cpu-temp-source ccd-avg -power rotate
.\trcc-mini.exe run -gpu -cpu-temp-source ccd1 -power replace
.\trcc-mini.exe run -gpu -power replace             # PA120: watts instead of temperature
.\trcc-mini.exe run -gpu -power rotate -power-period 4s -power-color ff9a00
.\trcc-mini.exe once -pm 16 -gpu -dry-run -temp 58 -cpu-power 185 -power replace
.\trcc-mini.exe run -pm 3 -link static -color ff7700 # AX120 example
.\trcc-mini.exe once -pm 16 -temp 55 -dry-run       # no USB writes
```

**Stop all vendor TRCC background processes** before `run` / `probe`,
including its USB helper processes, so both programs do not compete for HID.
`detect` is read-only and works while TRCC runs.

The handshake may only respond once per USB power cycle; `probe` stores
the PM model to the user's AppData configuration, allowing subsequent
`run` calls without repeating it. When original TRCC has already consumed
the handshake, physically power-cycle the device first, or specify the known
PM from its model (use `-pm`, *not* the style ID). A temporary example
`-pm 16` must **not** be used unless the device truly is PA120 Digital.

`run` is foreground, Ctrl+C to stop. For auto-start use Windows Task Scheduler.

## Windows CPU sensors

- CPU load: native `GetSystemTimes`, no driver or process required.
- GPU load/temperature (optional): native NVML calls to the NVIDIA driver.
  **Disabled by default** to minimize memory. Enable with `-gpu`, or
  `-link gpu-temp`/`-link gpu-load` to request GPU-linked RGB.
  NVML initialization was measured at approximately **30 MiB working set**
  on the test machine, compared with approximately **9 MiB** for CPU-only.
- AMD Ryzen CPU Tctl: signed official PawnIO AMD Family17-1Ah module, embedded
  in the EXE and loaded through `DeviceIoControl`.
- Ryzen Zen 4/5 also exposes independent per-CCD chiplet temperatures.
  Use `-cpu-temp-source tctl` (default), `ccd1`, `ccd2`,
  `ccd-max` (hottest available CCD), or `ccd-avg` (average).
  On Ryzen 9950X, these read SMN registers `0x59B08 + 4*n`
  through PawnIO using `(raw & 0xfff)/8 - 305`.
  Run `trcc-mini sensors` to compare actual readings without USB writes.
  These CCD readings are NOT the separate ASUS motherboard `CPU Diode`
  sensor, which requires a distinct board-specific sensor interface.
- AMD Ryzen CPU package watts: read `MSR 0xC0010299` for the energy unit
  and `0xC001029B` for the package-energy counter, then divide the
  rollover-safe energy difference by elapsed time (first sample warms up).
- NVIDIA GPU watts: `nvmlDeviceGetPowerUsage`, milliwatts converted to watts.
  On modern GPUs the value may be a smoothed one-second average rather
  than instantaneous PCIe/PSU consumption.
- A **one-time installation of the official signed PawnIO kernel driver**
  from <https://pawnio.eu/> plus Administrator privilege is required to
  measure CPU temperature. This program does **not** install or bundle the
  kernel driver; it does not require HWiNFO, Python, .NET, WinRing0, or
  LibreHardwareMonitor. The standalone driver remains an OS-level prerequisite.
- Without PawnIO, temperature is unavailable, so temperature digits are
  left blank. The command never displays a fabricated 0°C. Use `-temp 55`
  for **test data only**.
- Current builds implement the CPU temperature/load digit layouts for styles
  1/2/3/5/6/7/8/13, and optional GPU zones for the same styles.
  Devices that primarily display disk, RAM or clock values
  (styles 4/9/10/11/12) require another metric mapping pass.

### Three-digit watts display

On PA120 (style 2), the physical two-row, four-value layout contains
3 CPU temperature digits + 2 CPU load digits on the top row and
3 GPU temperature digits + 2 GPU load digits on the bottom row.
The three temperature digits can hold watts (`0..999`) with no
frame-size change, while both percent digits remain visible.
Use `-power rotate` to show temperatures and watts in turns (default 4s);
`-power replace` shows watts continuously instead of temperatures.
While displaying watts, the Celsius indicator is **off**, and the
three-digit numbers are amber (`-power-color` is customizable).
There is no dedicated **W** indicator on this physical profile; color
and the alternating interval distinguish units. If a sensor is absent,
its temperature digits remain in temperature mode instead of showing
fake 0W.

AK120 (style 3) and LF8/LF12 (styles 5/6) have dedicated 3-digit
watts fields: with `-power rotate` or `-power replace`, they show
available watt values in those fields without replacing temperatures.
Both kinds of temperature/power readings use the same single process.
The live USB model has NOT been probed while the original vendor app runs,
so the PA120 PM value must NOT be assumed for this unit without verification.

`-cpu-power` / `-gpu-power` provide dry-run-only test values and are
rejected for real USB output.

LED frames are sent only on change, at 1Hz default sampling; each complete
frame respects a 30ms firmware cooldown. HID transfers use cancellable
overlapped I/O with explicit timeout. Failed transfers are retried via reopen.
No GUI, HTTP server, subprocess, runtime Python module or external DLL is used.

## Verification status

- Windows compile: tested
- Windows USB discovery on physical `0416:8001`: tested (read-only)
- Protocol packet generation: locally tested, no USB writes
- Live device display/update: **not verified** until vendor software exits
- Ryzen temperature path: **not verified** because PawnIO driver is not installed
- CPU stress/resume durability: **pending**

## Licensing

Ported code and mapping tables: GPL-3.0-or-later; see `LICENSE`.
Signed AMD module: namazso/PawnIO.Modules 0.2.11, LGPL-2.1; see `NOTICE.md`.
