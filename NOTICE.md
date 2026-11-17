# Attribution and dependency notices

TRCC Mini is a native Windows CLI port of the HID LED protocol and
segment-display mappings from
[Lexonight1/thermalright-trcc-linux](https://github.com/Lexonight1/thermalright-trcc-linux).
Original project copyright its respective contributors; GPL-3.0.
The port is GPL-3.0-or-later and must retain GPL licensing when distributed.

The embedded `AMDFamily17.bin` module is the **official signed** upstream
PawnIO.Modules 0.2.11 release (namazso/PawnIO.Modules). SHA-256:
`dae74615761b78bdf064dfb3e136252ddcc6fc727d88f14738d0e5800d427a91`.
PawnIO.Modules uses LGPL-2.1; it is a separately signed hardware module,
loaded through a Windows device IO control. It requires the standalone signed
PawnIO kernel driver to have been installed by the system administrator.
No driver binary or installer is embedded and no driver is installed by this program.

The software does not run Python, .NET, HWiNFO or external helper processes.
