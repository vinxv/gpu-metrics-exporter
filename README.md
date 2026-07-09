# gpu-metrics-exporter

[![CI](https://github.com/vinxv/gpu-metrics-exporter/actions/workflows/ci.yml/badge.svg)](https://github.com/vinxv/gpu-metrics-exporter/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/vinxv/gpu-metrics-exporter)](https://github.com/vinxv/gpu-metrics-exporter/releases/latest)
[![Go Report Card](https://goreportcard.com/badge/github.com/vinxv/gpu-metrics-exporter)](https://goreportcard.com/report/github.com/vinxv/gpu-metrics-exporter)
[![Go Version](https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Prometheus Exporter](https://img.shields.io/badge/Prometheus-exporter-E6522C?logo=prometheus&logoColor=white)](https://prometheus.io)
[![Platform: Linux](https://img.shields.io/badge/platform-linux-00ADD8?logo=linux&logoColor=white)](https://www.kernel.org)

通用的、可配置的 Prometheus GPU 指标导出器。通过 YAML 配置文件即可适配任意 GPU 品牌，无需修改代码。

目前已内置 5 种国产 GPU 的适配配置：**华为昇腾**、**燧原**、**天数智芯**、**海光**、**PPU**。

## 设计理念

核心思路是 **配置驱动**——所有 GPU 品牌相关的逻辑（命令、解析规则、指标映射）都定义在 YAML 配置文件中。导出器本身是一个通用的引擎，负责：

1. 周期性执行 SMI 命令
2. 按配置的提取规则解析输出
3. 将结果以 Prometheus 格式暴露

添加新的 GPU 品牌只需编写一份 YAML 配置文件，无需改动 Go 代码。

## 快速开始

```bash
# 编译
go build -o gpu-metrics-exporter .

# 编译并验证所有内置配置
make validate

# 校验单个配置（类似 nginx -t）
./gpu-metrics-exporter validate -config configs/ascend.example.yaml

# 试运行一次，查看提取结果（不启动服务）
./gpu-metrics-exporter test -config configs/ascend.example.yaml

# 以 JSON 格式查看提取结果（便于脚本处理）
./gpu-metrics-exporter test -config configs/ascend.example.yaml --format json

# 启动服务
./gpu-metrics-exporter run -config configs/ascend.example.yaml

# 启动时覆盖监听地址
./gpu-metrics-exporter run -config configs/ascend.example.yaml -listen 0.0.0.0:9090
```

## 命令速览

| 命令 | 说明 |
|------|------|
| `run` | 启动指标导出服务 |
| `validate` | 校验配置文件语法和语义（不连接 GPU） |
| `test` | 执行一次采集并以表格或 JSON 输出提取结果 |
| `gen-htpasswd` | 生成 bcrypt 密码哈希 |
| `version` | 打印构建版本号 |

## 支持品牌

| 品牌 | SMI 工具 | 示例配置 | 指标前缀 |
|------|----------|---------|---------|
| 昇腾 | `npu-smi` | `configs/ascend.example.yaml` | `npu_*` |
| 燧原 | `efsmi` | `configs/enflame.example.yaml` | `gcu_*` |
| 天数智芯 | `ixsmi` | `configs/illuvatar.example.yaml` | `gpu_*` |
| 海光 | `hy-smi` | `configs/hygon.example.yaml` | `hcu_*` |
| PPU | `ppu-smi` | `configs/ppu.example.yaml` | `ppu_*` |

## 配置

按 GPU 品牌选择对应的示例配置，修改 `command` 路径即可使用。

### 最小配置

```yaml
command: "npu-smi info watch -s ptaicmb -d 5"
timeout: 10s
interval: 15s
listen: "127.0.0.1:9810"

labels:
  - name: brand
    value: "ascend"

metrics:
  - name: npu_power_watts
    help: "设备功耗（瓦）"
    type: gauge
    kind: columnar
    aliases:
      - device_power_watts
    columnar:
      column_name: "Pwr(W)"
      device_column: 0
      unavailable_values: ["-", "N/A"]
```

### 指标别名（跨品牌通用指标）

通过 `aliases` 将不同品牌的相同语义指标映射到统一名称，便于跨品牌统一查询：

```yaml
metrics:
  - name: npu_power_watts        # 品牌原始名称（始终输出）
    aliases:
      - device_power_watts       # 跨品牌通用名称（副本）
```

按品牌查询：`npu_power_watts{brand="ascend"}`
跨品牌查询：`device_power_watts`

> 主名称保留品牌原始指标名，别名仅给语义相同的指标添加副本，两者同时输出。

### 提取模式

| 模式 | 适用场景 | 配置键 |
|------|---------|-------|
| `columnar` | 表格输出，有表头（大多数 SMI 工具） | `columnar` |
| `regex` | 逐行输出，正则捕获 | `regex` |
| `js` | 复杂格式，需编程逻辑解析 | `js` |

#### 列式提取（columnar）

最常用模式，适用于 `npu-smi`、`ixsmi`、`efsmi` 等工具的表格输出。

```yaml
metrics:
  - name: gpu_temperature_celsius
    kind: columnar
    columnar:
      column_name: "gtemp"         # 表头中的列名
      device_column: 0             # 设备 ID 所在列（0-based）
      skip_lines: 2                # 跳过前置行数（如版权横幅）
      unavailable_values: ["-", "N/A"]
```

#### 正则提取（regex）

适用于每行对应一个数据点的格式。

```yaml
metrics:
  - name: gpu_power_watts
    kind: regex
    regex:
      pattern: 'GPU\s+(\d+)\s*:\s*([\d.]+)\s*W'
      device_group: 1              # 设备 ID 是第几个捕获组（1-based）
      value_group: 2               # 值是第几个捕获组
```

#### JS 提取（js）

适用于需要复杂解析逻辑的场景。脚本运行在 Goja（Go 实现的 JavaScript 虚拟机）沙箱中，通过 `emit()` 函数输出数据点。

```yaml
metrics:
  - name: gpu_power_watts
    kind: js
    js:
      script: |
        var lines = input.split('\n');
        for (var i = 0; i < lines.length; i++) {
            var parts = lines[i].trim().split(/\s+/);
            if (parts.length >= 2) {
                emit('gpu_power_watts', parseFloat(parts[1]), {device: parts[0]});
            }
        }
```

### 全局标签

顶层 `labels` 注入到所有指标，用于区分品牌或附加集群信息：

```yaml
labels:
  - name: brand
    value: "ascend"
  - name: cluster
    value: "prod-gpu-01"
```

### 指标级自定义标签

单个指标可以通过 `labels` 覆盖或追加标签：

```yaml
metrics:
  - name: npu_power_watts
    labels:
      - name: component
        value: "ai-core"
```

> 指标级标签中与全局标签同名的会覆盖全局值。

### 认证与 IP 访问控制

```yaml
listen: "0.0.0.0:9810"
auth:
  username: "admin"
  password_hash: "$2a$10$..."   # 通过 gen-htpasswd 生成

# IP 白名单 / 黑名单
allowed_ips:
  - "10.0.0.0/8"
  - "127.0.0.1"
denied_ips:
  - "192.168.1.100"
```

IP 过滤规则：
- 仅配 `allowed_ips`：白名单模式，只有列表中的 IP 可访问
- 仅配 `denied_ips`：黑名单模式，除了列表中的 IP 外都可访问
- 两者都配：白名单优先（denied_ips 被忽略）
- 两者都不配：所有 IP 可访问

### 生成密码哈希

```bash
./gpu-metrics-exporter gen-htpasswd admin
# 输入密码 → 输出 bcrypt 哈希
```

## 端点

```
GET /metrics  → Prometheus 指标（受认证和 IP 过滤保护）
GET /healthz  → 健康检查
```

示例响应（Prometheus 文本格式）：

```
# HELP npu_power_watts NPU 瞬时功耗（瓦）
# TYPE npu_power_watts gauge
npu_power_watts{device="0",brand="ascend"} 95.3
npu_power_watts{device="1",brand="ascend"} 88.7
# HELP device_power_watts NPU 瞬时功耗（瓦）
# TYPE device_power_watts gauge
device_power_watts{device="0",brand="ascend"} 95.3
device_power_watts{device="1",brand="ascend"} 88.7
```

## 部署

### Systemd 服务

```bash
# 安装二进制和配置
mkdir -p /opt/gpu-metrics-exporter
cp gpu-metrics-exporter /opt/gpu-metrics-exporter/
cp configs/ascend.example.yaml /opt/gpu-metrics-exporter/config.yml

# 安装 systemd 服务
cp gpu-metrics-exporter.service /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now gpu-metrics-exporter
```

### Makefile 打包

```bash
# 跨平台编译（linux amd64 + arm64）并打包为 zip
make package
# 输出: gpu-metrics-exporter-<version>.zip
# 包含: 二进制文件、示例配置、README、systemd service 文件
```

> 二进制为静态编译（禁用 CGO、无 libc 依赖），可在 glibc / musl（Alpine）/ 旧版 glibc 系统上直接运行。

## 添加新 GPU 品牌

只需 3 步，无需修改代码：

1. 在 `configs/` 目录下新建 `your-brand.example.yaml`
2. 填写 `command`（SMI 工具命令）、`brand` 标签、需要采集的 `metrics`
3. 根据 SMI 输出格式选择合适的提取模式（通常是 `columnar`）

完成后运行验证：

```bash
./gpu-metrics-exporter validate -config configs/your-brand.example.yaml
./gpu-metrics-exporter test -config configs/your-brand.example.yaml
```

## 项目结构

```
gpu-metrics-exporter/
├── main.go              # 入口，命令行解析
├── internal/
│   ├── config/          # YAML 配置加载与校验
│   ├── collector/       # Prometheus Collector 实现
│   ├── executor/        # SMI 命令执行器
│   ├── extractor/       # 三种提取模式（columnar / regex / js）
│   ├── model/           # 数据模型
│   └── server/          # HTTP 服务（认证、IP 过滤、优雅关闭）
├── configs/             # GPU 品牌适配配置
│   ├── ascend.example.yaml
│   ├── enflame.example.yaml
│   ├── illuvatar.example.yaml
│   ├── hygon.example.yaml
│   └── ppu.example.yaml
├── kb/                  # 各品牌 SMI 工具输出样例（用于开发调试）
├── deploy/              # 部署资源
├── Makefile             # 编译、测试、打包
└── gpu-metrics-exporter.service  # Systemd 单元文件
```

## License

本项目基于 [MIT License](LICENSE) 开源。
