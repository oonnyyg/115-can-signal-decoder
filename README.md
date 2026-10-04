# CAN 信号解码工具

离线将车辆采集的原始 CAN 报文（JSONL）按 JSON 信号目录解释为工程量。
仅使用 Go 标准库，不连接车辆，不实现 DBC 全语法。

## 使用

```sh
go run . -catalog catalog.json -input log.jsonl -summary summary.json
```

- `-catalog`：信号目录 JSON（必填）。
- `-input`：JSONL 报文文件，缺省读标准输入。
- `-summary`：汇总统计输出文件，缺省写标准错误。

解码结果按输入顺序逐行写标准输出（JSONL）；未知帧、坏报文等错误
逐行写标准错误（`{"line":N,"error":"..."}`），不阻断后续记录。

## 输入报文格式（JSONL）

```json
{"id": "0x100", "data": "FEAF010001000000"}
{"id": 256, "data": "FEAF010001000000"}
```

- `id`：十一位标准帧标识，数字或 `0x` 十六进制字符串。
- `data`：十六进制载荷，字节数必须与目录声明的 `dlc` 一致。

## 信号目录格式

```json
{
  "frames": [
    {
      "id": 256,
      "name": "Status",
      "dlc": 8,
      "signals": [
        {"name": "temp", "start": 0, "length": 12, "signed": true,
         "factor": 0.1, "offset": -40, "min": -40, "max": 100}
      ],
      "mux": {
        "name": "mode", "start": 16, "length": 2,
        "branches": {
          "1": [{"name": "speed", "start": 24, "length": 16, "factor": 0.01}],
          "2": [{"name": "torque", "start": 24, "length": 16, "signed": true}]
        }
      }
    }
  ]
}
```

帧字段：

- `id`：十一位标准帧标识（0..0x7FF），目录内不得重复。
- `dlc`：载荷字节数，1..8。
- `signals`：普通信号列表，每条报文始终解码。
- `mux`：可选复用定义，至多一个。

信号字段：

- `name`：帧内唯一。
- `start`：起始位，小端位编号（首字节最低位为 0），允许跨字节。
- `length`：位长，正整数；位段必须落在 `dlc` 字节载荷内。
- `signed`：true 表示二进制补码；先解释符号，再按
  `value = raw * factor + offset` 换算工程量（`factor` 缺省 1，
  `offset` 缺省 0）。
- `min`/`max`：可选闭区间范围，须成对出现；超界仅在输出中标记
  `out_of_range: true`，不丢弃原始值。

复用（mux）：

- 选择信号为无符号整数，按原始整数值激活 `branches` 中同名分支。
- 不同分支可以复用相同位段；同一分支内部、分支与普通信号或选择
  信号之间的重叠会被拒绝。
- 报文的选择值没有匹配分支时，仍输出普通信号，并标记
  `"mux_known": false`，不使用其他分支补齐。

## 输出

每条解码记录：

```json
{"line":1,"id":256,"frame":"Status","mux_known":true,"mux_value":1,
 "signals":[{"name":"temp","raw":-2,"value":-40.2,"out_of_range":true},
            {"name":"speed","raw":256,"value":2.56,"mux_value":1}]}
```

汇总统计包含：记录数、解码数、错误数、各信号解码数量与超界数量，
以及全部已解码工程量中的最小值和最大值。

## 测试

```sh
go test ./...
```

覆盖负数补码、跨字节位段、复用分支切换、未知分支、超界标记、
坏报文定位与目录校验。`examples/` 目录提供可运行的样例。
