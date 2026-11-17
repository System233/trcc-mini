# TRCC Mini — Windows HID 数码屏 CLI

轻量、原生、单文件的 Thermalright TRCC 数码屏控制程序。运行时不需要 Python、.NET、GUI、Node 或独立配置服务。源代码仅使用 Go 标准库，支持 Windows x64，USB 设备 VID:PID 为 `0416:8001`。

协议、LED 段码映射源自开源项目 [thermalright-trcc-linux](https://github.com/Lexonight1/thermalright-trcc-linux)（GPL-3.0）。

## 快速开始

```powershell
.\trcc-mini.exe config init     # 首次创建 EXE 旁的 trcc-mini.json
notepad .\trcc-mini.json       # 编辑温度源、GPU、功耗、颜色/主题
.\trcc-mini.exe config check   # 检查配置，不访问 USB
.\trcc-mini.exe run            # 按配置运行；Ctrl+C 停止
```

已识别的设备（例如 PM=16 的 PA120 Digital）自动使用设备缓存，无须在运行命令中重复指定型号。若已有配置，`config init` 会拒绝覆盖。

其他命令：

```powershell
.\trcc-mini.exe config path                 # 显示正在使用的 JSON 路径
.\trcc-mini.exe config show                 # 显示有效配置（包括默认值）
.\trcc-mini.exe once                        # 按每个区域的第一个有效指标生成一帧，不写 USB
.\trcc-mini.exe run -v                      # 输出详细传感器读数
.\trcc-mini.exe sensors                     # 只读传感器诊断，不访问 USB
.\trcc-mini.exe detect                      # 只读 USB 枚举
.\trcc-mini.exe run --config C:\temp\my-trcc.json
.\trcc-mini.exe run -c C:\temp\my-trcc.json -v
.\trcc-mini.exe probe                       # 仅未识别硬件时执行，通常只握手一次
```

**参数规范：** `-c`、`-n`、`-v`、`-h` 是单字符短参数；对应长参数为 `--config`、`--dry-run`、`--verbose`、`--help`。传感器、GPU、功耗、亮度和主题不提供命令行参数，只在 JSON 中配置。

默认配置文件 `trcc-mini.json` 位于 EXE 同目录；启动前必须存在，可通过 `config init` 创建。用户也可以通过 `--config FILE` 指定另一个位置，不进行隐式查找或迁移。设备握手缓存 `device.json` 位于 Windows 用户配置目录，是**另一份自动生成的文件**，不要用它配置主题。

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
      "symbols": ["C", "F"],
      "color": {"mode": "solid", "value": "#FFB000"}
    },
    "gpu.power": {
      "symbols": ["C", "F"],
      "color": {
        "mode": "threshold",
        "stops": [
          {"at": 0, "color": "#32D583"},
          {"at": 100, "color": "#FFB020"},
          {"at": 200, "color": "#FF4040"}
        ]
      }
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
- `sensors.cpuTemperature`：`tctl`、`ccd1`、`ccd2`、`ccd-max`、`ccd-avg`。`tctl` 是 AMD 散热控制温度；CCD 是芯粒温度，**不是主板 CPU Diode**。不要用固定温差伪造二极管读数。
- `sensors.gpu`：PA120 的 GPU 行默认开启；使用 NVIDIA NVML 读取温度、负载、功耗，不需要单独程序。
- 真实 CPU 温度与功耗需要 Windows 的 [PawnIO](https://pawnio.eu/) 签名内核驱动及足够的访问权限。随 EXE 内置已签名的 AMD 模块，但不安装内核驱动，也不依赖 HWiNFO。
- `sensors` 命令仅打印可用的温度/功耗读数，方便核对来源。

### 显示区域 / 独立指标轮换

- `display.slots`：PA120 的四个物理位置，`cpu.primary`（上排三位）、`cpu.secondary`（上排两位）、`gpu.primary`（下排三位）、`gpu.secondary`（下排两位）。
- `metrics`：按顺序列出一个或多个指标，包括 RAM、VRAM 和下方定义的派生指标；可以跨 CPU/GPU 区域选用数据。
- `interval`：只对包含多个指标的区域有意义；例如 `4s` 表示每隔四秒切换到下一个可用指标。每个区域拥有自己的周期。
- **固定显示：** `"metrics": ["gpu.power"]`；**温度/功耗轮换：** `"metrics": ["cpu.temperature", "cpu.power"], "interval": "4s"`。无需任何全局功耗模式。
- 轮换自动跳过本次不可用、超出该区域数字位数的指标。若全部不可用，该位置熄灭，绝不显示伪造的 `0`。
- 三位数区域可显示 `0..999`，两位数区域可显示 `0..99` 和专用 LED 支持的 `100`。**数字硬件不具有小数点，实际显示会四舍五入为整数**；例如 4.5 GiB 在面板上显示为 5，但诊断日志仍显示真实小数。180W 无法在两位数区域正确表达时会跳过；不会错误地裁剪成 100W。温度/百分比指示符跟随**当前实际指标**点亮，没有专用 W 符号。
- CPU/GPU 铭牌是固定的物理 LED；该行只要仍有对应本机 CPU/GPU 来源的有效指标就会保留名称灯。当该行两个区域都显示另一颗设备的来源时，关闭原铭牌，避免错误标注数据来源。
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
| `gpu.memory.used` / `gpu.memory.total` | GiB，NVML 显存已用/总容量 |
| `gpu.memory.load` | %，显存使用率 |
| `system.power.estimated` | W，**仅在 `derived` 明确配置后可用**，且为估算，不是墙上插座实测 |

每个指标在 `indicators` 下独立设置，不依赖其被分配到哪个物理位置。**当前指标级颜色与符号渲染适用于 PA120 Digital（style 2）**，其他显示布局仍使用各自的原生渲染方式：

- `color.mode: "solid"`：使用 `value: "#RRGGBB"` 固定 RGB 颜色。
- `color.mode: "gradient"`：`stops` 按 `at` 数值递增，两个相邻色标之间线性插值。
- `color.mode: "threshold"`：`stops` 按 `at` 数值递增；达到阈值立即换色，不插值。阈值边界属于新的色阶。
- `unit`：温度可用 `C`、`F`（**数值也会摄氏转华氏**）；内存/显存可用 `GiB`、`MiB`。百分比和瓦数只使用自身原生单位。颜色阈值参照**换算后的显示数值**。
- `symbols`：硬件符号灯组合。例如 `"symbols": ["C", "F"]` 可以在功耗数字旁边同时点亮 C 与 F 作为自定义标识；**这只是 LED 标记，不意味着功耗被换算为温度**。支持 `C`、`F`、`%`，明确指定空数组 `[]` 表示不点亮任何单位灯。
- 三位数区域具有 C/F 灯，二位数区域只有 % 灯。配置与物理区域不匹配时 `config check` 会报错；设备上**没有 W、GiB 或 MiB 的专用符号**。无法显示的位数会跳过，不会截断数值。

### 派生指标

`derived` 使用 `operation: "sum"` 和 `sources` 定义多个瓦数指标的加总，`offset` 是你手动估计的其他系统负载功耗（默认 0W）。可以递归引用其他派生项，但不允许循环依赖、重复计入同一输入、或混合温度与功耗。

例如：`cpu.power + gpu.power + offset` 可定义 `system.power.estimated`，但**CPU 封装+GPU 功耗不等于整机输入功耗**。主板、内存、SSD、风扇、PSU 损耗等通常没有直接测量；若没有外接功率计，不能将其标为真实整机功耗。任一必要源无效时，派生结果也为 N/A，而非部分求和。

如需显示它，将 `"system.power.estimated"` 加入任一三位槽位的 `metrics` 数组。派生名称必须以 `.estimated` 结尾，明确表达估算语义。

### 颜色 / 主题

- `theme.preset`：`minimal`（CPU 青蓝、GPU 紫）、`thermal`（按温度/负载数值平滑渐变）、`night`（深色低刺激）、`custom`（使用 `theme.color` 作为各区基础色）。目前仅实现静态和数据联动灯效，不包含彩虹/呼吸动画。
- `theme.zones`：可分别配置 `cpu.temp`、`cpu.load`、`gpu.temp`、`gpu.load`。未覆盖的字段继承所选主题。
- 每区 `mode`：`solid`、`temp` 或 `load`；`temp` 和 `load` 用该区域的 CPU/GPU 数据计算线性渐变。一个数字的所有笔画保持同色。
- `theme.link` 是非四区域设备的全局颜色联动方式；若在 PA120 上设为非 `static`，则切换到全局统一颜色模式；**但 `indicators.*.color` 的显式设置始终具有最高优先级**。
- `powerColor` 可分别为**四个物理区域**的功耗数字指定颜色；配置 `cpu.secondary`、`gpu.secondary` 区域的颜色也有效。颜色格式为 RGB 十六进制，例如 `#FFB000`。

配置在 **启动时读取**；编辑 `trcc-mini.json` 后重启 CLI 生效。无需安装配置守护服务，也不会额外产生后台进程。

## 安全与验证

在使用 `run` 或 `probe` 发送真实 HID 数据前，请先退出原厂 TRCC 和相关 USB helper，避免两个程序争抢设备。已缓存的 PM 无需再次探测；某些型号的握手在一个 USB 上电周期内可能只响应一次。

`config init/show/check/path`、`sensors` 不操作 USB；`detect` 只枚举 USB；`once` 或 `run -n` 完全不写入 USB，适合先验证 JSON 和渲染。

`once` 在任一区域需要 CPU 功耗时先读取一次 AMD 能量计数器作为基线，
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

GPL-3.0-or-later 源代码许可见 `LICENSE`；内置 AMD 模块的 LGPL-2.1 信息见 `LICENSE.PawnIO.Modules` 与 `NOTICE.md`。
