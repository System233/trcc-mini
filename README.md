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
.\trcc-mini.exe probe                              # handshake ONCE, save PM cache
.\trcc-mini.exe run -pm 16 -link load              # PA120 example: CPU% linked RGB
.\trcc-mini.exe run -pm 16 -link temp              # CPU °C linked RGB
.\trcc-mini.exe run -pm 16 -gpu -link gpu-temp     # optional RTX 4070 GPU
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
