# TRCC Mini — 跨平台 USB HID 数码屏 CLI

轻量、原生、单文件的 Thermalright TRCC 数码屏控制程序。运行时不需要 Python、.NET、GUI、Node 或独立配置服务。公共核心仅使用 Go 标准库，USB 设备 VID:PID 为 `0416:8001`。Windows 已有实机开发基线；Linux 实现原生 hidraw/hwmon/systemd，macOS 提供实验性的 IOKit/launchd 适配。

协议、LED 段码映射源自开源项目 [thermalright-trcc-linux](https://github.com/Lexonight1/thermalright-trcc-linux)（GPL-3.0）。

## 快速开始

```powershell
.\trcc-mini.exe config init     # 首次创建 EXE 旁的 trcc-mini.json
notepad .\trcc-mini.json       # 编辑温度源、GPU、功耗、颜色/主题
.\trcc-mini.exe config check   # 检查配置，不访问 USB
.\trcc-mini.exe run            # 按配置运行；Ctrl+C 停止
.\trcc-mini.exe service install # 管理员终端安装开机自启 Windows 服务
.\trcc-mini.exe service start   # 启动服务
```

已识别的设备（例如 PM=16 的 PA120 Digital）自动使用设备缓存，无须在运行命令中重复指定型号。若已有配置，`config init` 会拒绝覆盖。

其他命令：

```powershell
.\trcc-mini.exe config path                 # 显示正在使用的 JSON 路径
.\trcc-mini.exe config show                 # 显示有效配置（包括默认值）
.\trcc-mini.exe once                        # 按每个区域的第一个有效指标生成一帧，不写 USB
.\trcc-mini.exe run -v                      # 输出详细传感器读数
.\trcc-mini.exe sensors                     # 只读传感器诊断，不访问 USB
.\trcc-mini.exe sensors list                # 枚举 CPU/GPU 及全部可配置指标
.\trcc-mini.exe sensors list --json         # 机器可读，缺失读数为 null
.\trcc-mini.exe detect                      # 只读 USB 枚举
.\trcc-mini.exe run --config C:\temp\my-trcc.json
.\trcc-mini.exe run -c C:\temp\my-trcc.json -v
.\trcc-mini.exe probe                       # 仅未识别硬件时执行，通常只握手一次
.\trcc-mini.exe service status              # 查询服务状态
.\trcc-mini.exe service stop                # 停止服务
.\trcc-mini.exe service uninstall           # 卸载服务注册（不删除配置）
```

**参数规范：** `-c`、`-n`、`-v`、`-h` 是单字符短参数；对应长参数为 `--config`、`--dry-run`、`--verbose`、`--help`。传感器、GPU、功耗、亮度和主题不提供命令行参数，只在 JSON 中配置。

默认配置文件 `trcc-mini.json` 位于可执行文件同目录；启动前必须存在，可通过 `config init` 创建。用户也可以通过 `--config FILE` 指定另一个位置，不进行隐式查找或迁移。设备握手缓存 `device.json` 位于系统返回的用户配置目录，是**另一份自动生成的文件**，不要用它配置主题。**后台服务以独立身份运行，必须在 `device.pm` 中明确配置型号，不能依赖交互用户的设备缓存。**

## 配置字段（示例）

```json
{
  "version": 3,
  "device": { "pm": 0 },
  "sensors": {
    "cpuTemperature": "ccd-avg",
    "gpu": true
  },
  "display": {
    "interval": "1s",
    "phaseInterval": "3s",
    "brightness": 75,
    "rowLabels": {"cpu": "auto", "gpu": "auto"},
    "slots": {
      "cpu.primary": {
        "metrics": ["cpu.temperature", "cpu.power"],
        "interval": "4s"
      },
      "cpu.secondary": {
        "metrics": ["cpu.load"]
      },
      "gpu.primary": {
        "metrics": ["gpu.temperature", "gpu.power"],
        "interval": "4s"
      },
      "gpu.secondary": {
        "metrics": ["gpu.load", "gpu.memory.load"],
        "interval": "4s"
      }
    }
  },
  "indicators": {
    "gpu.temperature": {
      "color": {
        "mode": "gradient",
        "stops": [
          {"at": 35, "color": "#25D4E8"},
          {"at": 65, "color": "#32D583"},
          {"at": 85, "color": "#FF4040"}
        ]
      }
    },
    "cpu.power": {
      "symbols": ["CPU", "C", "F"],
      "color": {"mode": "solid", "value": "#FFB000"}
    },
    "cpu.load": {
      "symbols": ["CPU", "%"]
    },
    "gpu.power": {
      "symbols": ["GPU", "C", "F"],
      "color": {
        "mode": "threshold",
        "stops": [
          {"at": 0, "color": "#32D583"},
          {"at": 100, "color": "#FFB020"},
          {"at": 200, "color": "#FF4040"}
        ]
      }
    },
    "gpu.load": {
      "symbols": ["GPU", "%"]
    },
    "memory.used": {"unit": "GiB"}
  },
  "derived": {
    "system.power.estimated": {
      "operation": "sum",
      "sources": ["cpu.power", "gpu.power"],
      "offset": 0
    }
  },
  "theme": {
    "preset": "minimal",
    "color": "#FFFFFF",
    "powerColor": "#FF9A00",
    "link": "static",
    "zones": {
      "cpu.temp": { "color": "#36C9E8", "powerColor": "#FFB000" },
      "cpu.load": { "mode": "solid", "color": "#36C9E8" },
      "gpu.temp": { "color": "#A78BFA", "powerColor": "#FF9200" },
      "gpu.load": { "mode": "load", "color": "#A78BFA" }
    }
  }
}
```

`config init` 会生成更简洁的默认配置，以上示例展示自定义四区域的所有主要字段。配置可只写需要覆盖的字段，其余字段继承默认值；未知字段、无效颜色和不支持的选项会导致检查失败，不会悄然忽略。

### 硬件 / 温度

- `device.pm`：默认 `0` 表示使用已识别的设备缓存。可以手动填写确知的 PM 型号（如已确认 PA120 Digital 的 `16`）。
- `sensors.cpuTemperature`：`tctl`、`ccd1`、`ccd2`、`ccd-max`、`ccd-avg`。在 AMD CPU 上，`tctl` 是散热控制温度，CCD 是芯粒温度，**不是主板 CPU Diode**；在 Intel CPU 上，`tctl` 映射为 IA32_PACKAGE_THERM_STATUS 的真实封装温度，CCD 专有指标不可用。不要用固定温差伪造温度。
- `sensors.gpu`：PA120 的 GPU 行默认开启。Windows 支持 NVIDIA NVML 和 AMD 驱动自带的 ADL2 PMLog（只读、按 PCI 地址精确匹配；Intel GPU 的 IGCL 接口尚未集成）；Linux 支持 NVIDIA 驱动工具、AMD amdgpu 和 Intel i915/xe hwmon/sysfs；macOS 根据系统提供的可用传感器采集。缺少原生数据时返回 N/A。
- 真实 Windows CPU 温度与功耗需要 [PawnIO](https://pawnio.eu/) 签名内核驱动及足够的访问权限。随 EXE 内置 AMD AMDFamily17 和 Intel IntelMSR 签名模块，由实际 CPU 厂商选择；Intel 通过 MSR 温度寄存器与 RAPL 能耗计数器读取，不安装内核驱动，也不依赖 HWiNFO 或已停止维护的 Intel Power Gadget。
- `sensors` 命令仅打印可用的温度/功耗读数，方便核对来源。

### 显示区域 / 独立指标轮换

- `display.slots`：PA120 的四个物理位置，`cpu.primary`（上排三位）、`cpu.secondary`（上排两位）、`gpu.primary`（下排三位）、`gpu.secondary`（下排两位）。
- `metrics`：按顺序列出一个或多个指标，包括 RAM、VRAM 和下方定义的派生指标；可以跨 CPU/GPU 区域选用数据。
- `interval`：只对包含多个指标的区域有意义；例如 `4s` 表示每隔四秒切换到下一个可用指标。每个区域拥有自己的周期。
- **固定显示：** `"metrics": ["gpu.power"]`；**温度/功耗轮换：** `"metrics": ["cpu.temperature", "cpu.power"], "interval": "4s"`。无需任何全局功耗模式。
- 轮换自动跳过本次不可用、超出该区域数字位数的指标。若全部不可用，该位置熄灭，绝不显示伪造的 `0`。
- 三位数区域可显示 `0..999`，两位数区域可显示 `0..99` 和专用 LED 支持的 `100`。**数字硬件不具有小数点，实际显示会四舍五入为整数**；例如 4.5 GiB 在面板上显示为 5，但诊断日志仍显示真实小数。180W 无法在两位数区域正确表达时会跳过；不会错误地裁剪成 100W。温度/百分比指示符跟随**当前实际指标**点亮，没有专用 W 符号。
- CPU/GPU 铭牌是**每行共用的物理 LED**，而不是独立分配给两个数字区域。默认自动根据该行当前有效指标的来源亮起；跨 CPU/GPU 来源显示时不会错误点亮另一个设备名称。也可以用下面的 `display.rowLabels` 强制开启或关闭。
- `display.rowLabels`：`{"cpu":"auto","gpu":"auto"}`（根据当前有效指标自动显示）、`"on"`（即使传感器无数据也强制点亮该行铭牌）、`"off"`（即使某个指标请求铭牌，也强制关闭）。两个选项互不影响；省略时使用 `auto`。
- `display.slots` 为 PA120 专用显示布局，其他型号继续使用各自硬件布局，不套用 PA120 四区域规则。
- `display.interval`：采样间隔，例如 `1s`；仅帧内容变化时发送更新。
- `display.brightness`：0～100；底层 USB 协议还有固定的硬件色彩比例系数。
- AK120、LF8、LF12 具有独立功耗位，不受 PA120 的 `slots` 自定义分配影响。

### 指标目录、颜色和符号

每个指标先有一个唯一名称，再由 `display.slots.*.metrics` 引用：

| 指标 | 单位及含义 |
| --- | --- |
| `cpu.temperature` / `gpu.temperature` | °C，CPU/GPU 核心温度 |
| `cpu.load` / `gpu.load` | %，CPU/GPU 利用率 |
| `cpu.power` / `gpu.power` | W，CPU 封装 / GPU 读取到的功耗 |
| `memory.used` / `memory.total` | GiB，Windows 已用/总物理 RAM |
| `memory.load` | %，物理 RAM 使用率 |
| `gpu.memory.used` / `gpu.memory.total` | GiB，支持的平台/独显的显存已用/总容量（macOS Apple Silicon 不按独显 VRAM 处理） |
| `gpu.memory.load` | %，显存使用率 |
| `system.power.estimated` | W，**仅在 `derived` 明确配置后可用**，且为估算，不是墙上插座实测 |

#### 多 CPU/GPU 设备索引与精细指标

无需另加 `devices`、`sources` 等设备配置。直接使用 `cpu0`、`cpu1`、`gpu0`、`gpu1` 等物理编号；无数字后缀的 `cpu`、`gpu` 表示综合指标。

```powershell
trcc-mini sensors list
trcc-mini sensors list --json
```

Windows 从系统显示设备类枚举真实 PCI 显卡，按照 PCI 总线、设备、功能号排序；NVIDIA NVML 通过真实 PCI 位置精确绑定，不再直接把 NVML 的 GPU0 等同于 `gpu0`。不包含 USB 虚拟显示器。若 PCI 无法可靠匹配，则宁可保持 N/A，也不把另一块 GPU 的值赋给该编号。例如本机识别为 `gpu0 = RTX 4070`、`gpu1 = AMD Radeon 核显`；请以**当前机器**的枚举结果为准。CPU 是物理封装编号，不是逻辑处理器编号；单插槽时可使用 `cpu0`，多插槽温度/功耗在当前寄存器采集器无法确定归属时保持每插槽 N/A，不能把全系统值冒充某个 CPU。

| 指标示例 | 实际含义 / 单位 |
| --- | --- |
| `cpu0.temperature` / `cpu0.ccd0.temperature` | CPU 封装控制温度 / CCD0 温度，°C；仅有真实读数才有效 |
| `cpu0.temperature.max` | 当前能读取的封装/CCD 温度最高值，°C |
| `cpu0.power` / `cpu0.load` | CPU 封装功耗 W / 单插槽 CPU 负载 % |
| `cpu0.clock` / `cpu.clock.max` | 系统报告的平均/最高当前处理器时钟，MHz；Windows 的 Power API 值不是逐周期测得的有效频率 |
| `gpu0.temperature` / `gpu0.load` | 指定 GPU 温度 °C / 使用率 % |
| `gpu0.power` / `gpu0.power.limit` | GPU 当前功率 W / 驱动设置的功率上限 W，**两者不是同一指标** |
| `gpu1.power.gfx` / `gpu1.power.asic` / `gpu1.power.soc` | AMD 驱动分别报告的图形域 / ASIC / SoC 功率，W；各域可能与 CPU 封装重叠，不自动等价于独显板卡功耗 |
| `gpu1.power.board` | AMD 驱动明确提供板卡功率时才有效，W；仅此时可用于缺省的 `gpu1.power` |
| `gpu1.temperature.soc` / `gpu1.voltage` | AMD SoC 温度 °C / 图形域电压 V（依驱动实际支持） |
| `gpu0.clock.core` / `gpu0.clock.memory` | 核心、显存时钟 MHz |
| `gpu0.load.memory` / `gpu0.memory.load` | 显存控制器忙碌率 / 显存空间占用率，% |
| `gpu0.memory.used` / `gpu0.memory.total` | 专用显存 GiB，核显不虚构独立显存 |
| `gpu0.fan.load` / `gpu0.pcie.tx` / `gpu0.pcie.rx` | 风扇百分比 / PCIe MB/s |
| `gpu0.temperature.hotspot` / `gpu0.temperature.memory` | 驱动支持时的热点/显存温度；否则 N/A |

所有可配置的设备指标名称可通过 `sensors list` 查看。即使配置使用了该名称，也不表示当前型号/驱动一定支持该传感器。无索引综合指标遵循：功率和容量求和、温度和 GPU 负载取最高值、CPU 总负载使用系统提供的加权值、显存占用百分比使用同一组有完整已用/总量读数的设备加总后计算。**N/A 不参与聚合，只有全部无有效数据时才显示 N/A**。此规则不代表缺失数据的设备功率为真实的 0W。Windows 的 AMD ADL2 能提供温度、图形负载、时钟、电压及多种功率域；当只有 CPU/SoC 可能重叠的功率读数时，`gpuN.power` 仍为 N/A，不污染 `gpu.power`。Windows Intel GPU 的 IGCL 遥测尚未接入，Intel GPU 仍会在枚举中占用自身编号。

使用示例：将任意现有 `display.slots.gpu.primary.metrics` 设置为 `["gpu0.power", "gpu0.temperature"]`（并设置 `interval`），即可在同一三位数区域轮换**指定显卡**的实际功耗与温度。其他指标级符号、颜色配置照旧。三位数不能容纳 4300MHz 等四位频率时，该值不会被错误截断，现有显示选择器将跳过。

每个指标在 `indicators` 下独立设置，不依赖其被分配到哪个物理位置。**当前指标级颜色与符号渲染适用于 PA120 Digital（style 2）**，其他显示布局仍使用各自的原生渲染方式：

- `color.mode: "solid"`：使用 `value: "#RRGGBB"` 固定 RGB 颜色。
- `color.mode: "gradient"`：`stops` 按 `at` 数值递增，两个相邻色标之间线性插值。
- `color.mode: "threshold"`：`stops` 按 `at` 数值递增；达到阈值立即换色，不插值。阈值边界属于新的色阶。
- `unit`：温度可用 `C`、`F`（**数值也会摄氏转华氏**）；内存/显存可用 `GiB`、`MiB`。百分比和瓦数只使用自身原生单位。颜色阈值参照**换算后的显示数值**。
- `symbols`：支持全部五种可配置实体符号：`CPU`、`GPU`、`C`、`F`、`%`。例如 `"symbols": ["CPU", "C", "F"]` 在上排功耗数字旁同时显示 CPU 铭牌和 C+F 灯；`"symbols": ["GPU", "%"]` 在下排两位数显示 GPU 铭牌及百分比灯。**这只是 LED 标记，不意味着功耗被换算为温度。**
- `symbols` 未配置时，软件根据指标来源及数值单位选择符合物理位置的默认灯。明确指定 `"symbols": []` 表示**此指标不请求任何符号**，但同一行的另一项有效指标可能仍请求共用 CPU/GPU 铭牌。若希望整行无条件关闭名称灯，使用 `display.rowLabels.cpu/gpu: "off"`。
- **物理布局：** 上排 CPU 名称（LED 0/1）、下排 GPU 名称（LED 2/3）；上排三位数 C/F（LED 4/5）、上排两位数 %（LED 6）；下排三位数 C/F（LED 7/8）、下排两位数 %（LED 9）。CPU/GPU 名称可以由同一行的任一指标请求，但不可以调换到对面行。配置与物理区域不匹配时 `config check` 会报错；设备上**没有 W、GiB 或 MiB 的专用符号**。无法显示的位数会跳过，不会截断数值。
- **符号颜色：** 指标级 `color` 同样应用于当前数字区的 C/F/%；CPU/GPU 铭牌共享整行，如果两个指标都指定了颜色，优先使用该行三位数指标的显式颜色，其次是两位数指标，否则使用主题颜色。

### 派生指标

`derived` 使用 `operation: "sum"` 和 `sources` 定义多个瓦数指标的加总，`offset` 是你手动估计的其他系统负载功耗（默认 0W）。可以递归引用其他派生项，但不允许循环依赖、重复计入同一输入、混合温度与功耗、把 `gpu0.power.limit` 当作实际功耗，也不允许将 `gpu.power` 与 `gpu0.power` 或 `cpu.power` 与 `cpu0.power` 重复求和。

例如：`cpu.power + gpu.power + offset` 可定义 `system.power.estimated`，但**CPU 封装+GPU 功耗不等于整机输入功耗**。主板、内存、SSD、风扇、PSU 损耗等通常没有直接测量；若没有外接功率计，不能将其标为真实整机功耗。派生求和**忽略 N/A**，仅累加可用值并加上 `offset`；**全部源无效时结果才是 N/A**。缺失的功耗不会被当作零瓦实测，综合结果可能是不完整的估算。

如需显示它，将 `"system.power.estimated"` 加入任一三位槽位的 `metrics` 数组。派生名称必须以 `.estimated` 结尾，明确表达估算语义。

### 颜色 / 主题

- `theme.preset`：`minimal`（CPU 青蓝、GPU 紫）、`thermal`（按温度/负载数值平滑渐变）、`night`（深色低刺激）、`custom`（使用 `theme.color` 作为各区基础色）。目前仅实现静态和数据联动灯效，不包含彩虹/呼吸动画。
- `theme.zones`：可分别配置 `cpu.temp`、`cpu.load`、`gpu.temp`、`gpu.load`。未覆盖的字段继承所选主题。
- 每区 `mode`：`solid`、`temp` 或 `load`；`temp` 和 `load` 用该区域的 CPU/GPU 数据计算线性渐变。一个数字的所有笔画保持同色。
- `theme.link` 是非四区域设备的全局颜色联动方式；若在 PA120 上设为非 `static`，则切换到全局统一颜色模式；**但 `indicators.*.color` 的显式设置始终具有最高优先级**。
- `powerColor` 可分别为**四个物理区域**的功耗数字指定颜色；配置 `cpu.secondary`、`gpu.secondary` 区域的颜色也有效。颜色格式为 RGB 十六进制，例如 `#FFB000`。

### Windows 原生服务

使用 Windows Service Control Manager（SCM）管理**同一份**原生 Go EXE，不依赖额外运行时或独立代理：

```powershell
# 确认 trcc-mini.json 中 device.pm 是已确认的产品型号（PA120 为 16）
.\trcc-mini.exe config check
.\trcc-mini.exe service install          # 管理员终端；注册为自动启动
.\trcc-mini.exe service start            # 立即启动（安装默认不自动启动）
.\trcc-mini.exe service status           # running/stopped
.\trcc-mini.exe service stop
.\trcc-mini.exe service uninstall        # 必须先停止服务
```

如果配置不在 EXE 旁，安装时可使用 `service install -c C:\path\trcc-mini.json`。为了避免 LocalSystem 服务执行可被普通用户替换的开发目录程序，**安装命令会把当前 EXE 复制至受 Windows 权限保护的 `%ProgramFiles%\TRCCMini\trcc-mini.exe`**，服务运行该安装副本；JSON 继续使用安装时指定的绝对路径，保持可编辑及热重载。更新服务 EXE 时应先停止并卸载服务，再从新版程序重新安装；卸载不会删除 JSON 或已复制的 EXE。

Windows 服务使用 LocalSystem 账户，`device.pm: 0` 无法使用你的个人设备握手缓存，安装会直接拒绝；已确认的 PA120 Digital 请设置 `"device": {"pm":16}`，不会重复进行一次性 HID 探测。

服务运行日志写入 `%ProgramData%\TRCCMini\service.log`，包含配置重载结果、设备连接错误和启动/停止信息。服务响应 Windows 停止/关机通知并退出；USB 设备或传感器在启动阶段未就绪时会重试，而不是每次都停止服务。**不要同时启动普通 `run` 与 Windows 服务，也不要与原厂 TRCC 程序争抢同一 USB 设备。**

### Linux（amd64 / arm64）

Linux 原生后端采用 `/dev/hidraw*` 和 `HIDIOCGRAWINFO` 发现 `0416:8001`，直接发送/接收 HID 报文，使用零号 Report ID 加 64 字节负载；不依赖 libusb 或 cgo。

```bash
go build -trimpath -o trcc-mini .
./trcc-mini config init
# 已确认 PA120 Digital 的 JSON 配置 device.pm = 16
./trcc-mini config check
./trcc-mini detect
./trcc-mini once      # 只渲染，不写 USB
./trcc-mini run
```

普通桌面用户可通过 udev 授权当前登录会话访问设备：

```bash
sudo install -m 644 assets/99-trcc-mini.rules /etc/udev/rules.d/
sudo udevadm control --reload-rules
sudo udevadm trigger --subsystem-match=hidraw
```

Linux CPU 利用率取自 `/proc/stat`，RAM 取自 `/proc/meminfo`，AMD/Intel CPU 温度分别通过 `k10temp` / `coretemp` hwmon 标签读取。CPU 功耗优先尝试 RAPL `energy_uj` 的时间差，或支持的 hwmon 功率属性；无可用硬件读数时返回 N/A，不伪造数据。NVIDIA GPU 通过驱动附带的 `nvidia-smi`（可选）读取，缓存约 1 秒，后台异步刷新避免阻塞 200ms 显示帧；AMD GPU 通过 amdgpu DRM/sysfs 与 hwmon 读取；Intel 核显/Arc 通过 i915/xe 的设备级 hwmon 读取温度、能耗计数器并换算功率，能获得独立显存容量的 Arc 设备也提供 VRAM 指标。**多 GPU 设备以 DRM PCI 地址排序：Linux 也支持 `gpu0`、`gpu1`，NVIDIA 通过 PCI Bus ID 而非 NVML 索引选择，所有可用读数聚合为 `gpu.*`。** Intel GPU 的 `power1_max` 是功耗**上限**，不是当前功耗，不能作为采样结果；缺少 GPU 全局使用率计数器时不根据频率伪造负载。

systemd 服务直接复用 `run` 的热重载和停止信号处理。安装需要 root，且需预先准备有效 JSON（`device.pm` 不得为 0）：

```bash
sudo ./trcc-mini service install --config /etc/trcc-mini/trcc-mini.json
sudo ./trcc-mini service start
./trcc-mini service status
journalctl -u trcc-mini.service -f
sudo ./trcc-mini service stop
sudo ./trcc-mini service uninstall
```

服务安装时复制程序至 `/usr/local/libexec/trcc-mini/trcc-mini`，配置 systemd 单元 `/etc/systemd/system/trcc-mini.service`；启动后保持原 JSON 文件位置以便热更新。上述 `/etc/trcc-mini/trcc-mini.json` 仅是示例，必须由你提前创建；也可不提供 `--config`，使用 EXE 旁已有的 JSON。安装默认启用开机自启但不会立即启动。无桌面会话的主机可使用服务账户访问 HID，不要将 hidraw 设备直接设为世界可写。

### macOS（Intel / Apple Silicon，实验性）

```bash
# 在真实 macOS 主机使用 Xcode Command Line Tools 和 cgo 构建
CGO_ENABLED=1 go build -trimpath -o trcc-mini .
./trcc-mini config check
./trcc-mini detect
./trcc-mini once
```

原生 IOKit 通过 `IOHIDManager` 枚举设备，并使用 `IOHIDDeviceSetReport` 发送 Output Report；编译链接 Apple 自带的 IOKit 和 CoreFoundation 框架，无第三方 Go 包。**在非 macOS 主机通过 `CGO_ENABLED=0` 交叉编译得到的 macOS 程序仅支持配置/诊断，USB HID 命令会明确提示须在 macOS 原生 cgo 构建。**

CPU 负载取自 `sysctl kern.cp_time`；RAM 使用 `sysctl hw.memsize` 和 `vm_stat`，提供考虑可回收页的近似占用指标。获授权时，`powermetrics` 可用于部分 CPU/GPU 功率、CPU die 温度和 GPU 活跃度；权限或采样器不具备时返回 N/A。Apple Silicon 使用统一内存，**不将它误报为独立 GPU VRAM**，AMD CCD 专用温度也不做伪造。

macOS 服务使用 launchd LaunchDaemon，命令同为 `service install/start/stop/status/uninstall`，安装和启停需要 `sudo`。安装目标为 `/usr/local/libexec/trcc-mini/trcc-mini`，服务配置在 `/Library/LaunchDaemons/com.trcc-mini.display.plist`，日志为 `/var/log/trcc-mini.log`。

**验证边界：** Linux amd64/arm64、macOS amd64/arm64（无 cgo）已通过交叉编译；macOS IOKit 的原生 cgo 编译/链接、两个新平台的真实 USB 写入、传感器采样及 systemd/launchd 启停尚未在目标主机测试。仓库包含 `.github/workflows/cross-platform.yml`，用于在相应系统执行原生构建和测试。

### 配置热重载与失败保护

`run` 与 Windows SCM、Linux systemd、macOS launchd 的服务进程**共同支持配置热重载**，不需要重启或附加守护进程。每约 500ms 读取 `trcc-mini.json` 并比对内容哈希；检测到新内容后使用同一套严格 JSON Schema 和业务规则校验，成功才一次性切换有效配置。可热更新采样周期、亮度、CPU 温度源、GPU 开关、显示区域、轮换、符号、颜色、主题及派生指标。改变采样周期时自动重置采样计时器；GPU 从关闭切换到开启时，须成功准备该平台的 GPU 采集器后才提交新配置。

如果 JSON 不完整、某字段非法、文件被删除，则**拒绝本次重载，继续运行上一份有效配置**，并记录一条错误，不以默认配置覆盖现场值。**GPU 暂时不存在、驱动尚未加载或某个设备传感器不可用并不属于配置错误：允许启用该设置，显示 N/A，后台继续尝试重新连接。**编辑磁盘文件不会被程序自动回滚：请纠正 JSON 后再次保存。唯一明确不支持热切换的业务参数是 `device.pm`（硬件型号），修改时拒绝重载，需要停止并重启程序或服务。自定义配置文件路径只能在启动/安装时确定。

配置按内容哈希检测，即使编辑器保留文件时间戳也能识别有效修改。服务只负责自身显示循环，不会再次 HID 探测设备型号。

## 安全与验证

在使用 `run` 或 `probe` 发送真实 HID 数据前，请先退出原厂 TRCC 和相关 USB helper，避免两个程序争抢设备。已缓存的 PM 无需再次探测；某些型号的握手在一个 USB 上电周期内可能只响应一次。

`config init/show/check/path`、`sensors` 不操作 USB；`detect` 只枚举 USB；`once` 或 `run -n` 完全不写入 USB，适合先验证 JSON 和渲染。

`once` 在任一区域需要 CPU 功耗时先读取一次能耗计数器作为基线（如 Windows AMD/Intel MSR 或 Linux RAPL），
再间隔 500ms 采集下一次值；单帧预览显示每个区域的**第一个有效指标**。
普通 `run` 不增加该等待，
第一次 CPU 功耗采样属于预热，可能为 `N/A`；
后续周期才会得到实际瓦数。没有有效传感器数据时一律输出
`N/A`，而不是把无效数值报告成真实的 `0W`。

当前配置结构为 `version: 3`；不做运行时旧版配置迁移。

测试覆盖配置、严格 JSON 校验、CLI 参数拼写、PA120 四区域独立轮换与配色、100% 负载、无效传感器跳过、GPU 功耗、CPU 能耗计数器、段码及 HID 报文构造。真实设备写入、PawnIO 下的 CCD 传感器值仍需要管理员环境实测。

## 构建与许可

```powershell
go test ./...
go vet ./...
go build -trimpath -buildvcs=false -ldflags="-s -w" -o trcc-mini.exe .
```

GPL-3.0-or-later 源代码许可见 `LICENSE`；内置 AMD/Intel PawnIO 模块的 LGPL-2.1 信息见 `LICENSE.PawnIO.Modules` 与 `NOTICE.md`。
