# 第三方组件声明

本项目自身的代码按根目录 `LICENSE` 中的 MIT 许可证提供。第三方组件及其版权、许可声明继续按各自条款保留。

| 组件 | 用途 | 许可声明 |
| --- | --- | --- |
| `github.com/klauspost/compress` v1.20.0 | zstd 请求解码及相关压缩实现 | [原始 LICENSE](licenses/klauspost-compress.txt) |
| klauspost/compress 中的 `zstd/internal/xxhash` | zstd 校验实现 | [原始 xxhash LICENSE](licenses/xxhash.txt) |
| Go 运行时与标准库 | 预编译程序的运行时及标准库 | [Go LICENSE](licenses/go.txt) |

压缩库的完整上游 LICENSE 包括多个子组件的声明；其中并非所有子组件都被本程序使用。具体依赖版本和校验值见 `go.mod`、`go.sum`。

上游项目：https://github.com/klauspost/compress 。本目录保留的许可文本来自该固定版本及用于构建的 Go 工具链。
