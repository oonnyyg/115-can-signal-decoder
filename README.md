# CAN Signal Decoder

离线 CAN 信号解码工具：从 JSON 信号目录与 JSONL 原始报文生成可追溯的工程量解码结果。
仅使用 Go 标准库，不连接车辆，也不实现 DBC 全语法。

## 用法

```sh
go run . -catalog examples/catalog.json -input examples/messages.jsonl
# 或使用 stdin/stdout
cat messages.jsonl | go run . -catalog catalog.json > out.jsonl
```

参数：`-catalog`（必填，信号目录 JSON）、`-input`（JSONL 报文，默认 stdin）、
`-output`（解码结果，默认 stdout）。

## 目录格式

```json
{
  "frames": [
    {
      "id": 256,
      "name": "Status",
      "dlc": 8,
      "signals": [
        {"name": "temp", "start": 0, "length": 12, "signed": true,
         "factor": 0.1, "offset": -40, "range": [-40, 100]}
      ],
      "mux": {"name": "mode", "start": 22, "length": 2},
      "branches": [
        {"value": 0, "signals": [{"name": "torque", "start": 24, "length": 8, "signed": true}]},
        {"value": 1, "signals": [{"name": "level", "start": 24, "length": 8, "factor": 0.5}]}
      ]
    }
  ]
}
```

- 仅支持 11 位标准帧（`id` ≤ 0x7FF）与 1–8 字节载荷（`dlc`）。
- 位编号为小端：首字节最低位为位 0，信号从 `start` 向高位连续读取 `length` 位，允许跨字节。
- `signed` 为 true 时按二进制补码解释原始整数，**先解释符号再**按 `value = raw * factor + offset` 换算。
- `factor` 默认 1，`offset` 默认 0；`range` 为可选闭区间，超界仅标记 `out_of_range`，不丢弃值。
- `mux` 为可选的无符号选择信号；`branches` 按 mux 原始整数值激活对应分支信号。
  普通信号始终解码；不同分支可复用位段，但同一分支内、以及分支与普通信号/mux 之间不得重叠。
- 校验规则：帧 id 与帧内信号名不得重复；位长必须为正；位段必须位于载荷内。

## 报文输入

每行一条 JSON：`{"id": 256, "data": "fe0f406400000000"}`，`data` 为十六进制字符串，
字节数必须等于目录声明的 `dlc`。

## 输出

按输入顺序逐行输出 JSONL。解码成功行包含 `line`、`id`、`frame`、`mux_value`、
`signals`（含 `name`、`value`、`raw`、可选 `out_of_range`）；mux 无匹配分支时仍输出
公共信号并置 `mux_unknown: true`，不使用其他分支补齐。未知帧或坏报文输出带 `error`
与行号的记录，不阻断后续记录。最后一行为汇总：

```json
{"summary":{"records":4,"decoded":2,"errors":2,
  "signals":{"temp":{"decoded":2,"out_of_range":1}},
  "min":-40.2,"max":127}}
```

汇总包含各信号解码数量与超界数量，以及全部已解码工程量中的最小值与最大值。

## 测试

```sh
go test ./...
```

覆盖负数补码、跨字节信号、复用分支切换、未知分支、坏报文定位与目录校验。
