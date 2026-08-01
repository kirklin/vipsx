# vipsx

[![Go Reference](https://pkg.go.dev/badge/github.com/kirklin/vipsx.svg)](https://pkg.go.dev/github.com/kirklin/vipsx)
[![CI](https://github.com/kirklin/vipsx/actions/workflows/ci.yml/badge.svg)](https://github.com/kirklin/vipsx/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

[libvips](https://www.libvips.org/) 的 Go 绑定，在运行时从已安装的库推导而来。

[English](README.md) | 简体中文

## 概述

libvips 通过 GObject 类型系统描述自身的操作。vipsx 在运行时读取这份描述，而非逐个
封装操作，因此单一调用路径即可覆盖已安装 libvips 暴露的全部操作，包括本项目发布之
后新增的操作。

同一条调用路径之上提供两套 API：

- **类型化 API** —— 每个操作对应一个函数和一个选项结构体，由已安装的 libvips 生成。
  在 8.18 上为 330 个函数和 47 个枚举类型，全部为 Go 代码：不生成 C 代码，也不存在
  逐操作的 cgo 编译开销。
- **通用 API** —— `Call`，接受任意操作名并在运行时解析其签名，用于生成器尚未覆盖的
  操作。

## 环境要求

- libvips 8.14 及以上
- Go 1.24 及以上
- 启用 cgo，并具备 C 工具链

libvips 的最低版本由持续集成确定而非估计：Debian 12 提供 8.14，支持周期至 2028 年。

## 安装

```bash
brew install vips        # 或：apt install libvips-dev
go get github.com/kirklin/vipsx
```

在 macOS 上，需允许 cgo 传递 libvips 所使用的预处理器选项：

```bash
export CGO_CFLAGS_ALLOW=-Xpreprocessor
```

## 文档

API 参考：[pkg.go.dev/github.com/kirklin/vipsx](https://pkg.go.dev/github.com/kirklin/vipsx)

## 使用

```go
im, err := vips.LoadFile("photo.jpg")
if err != nil {
    return err
}
defer im.Close()

thumb, err := vips.ThumbnailImage(im, 640, &vips.ThumbnailImageOptions{
    Crop: vips.Ptr(vips.InterestingAttention),
})
if err != nil {
    return err
}
defer thumb.Close()

webp, err := vips.SaveBuffer(thumb, ".webp", vips.In("Q", 82))
```

### 类型化 API

```go
small, err := vips.Resize(im, 0.5, nil)
blur, err := vips.Gaussblur(small, 2.0, nil)
gray, err := vips.Colourspace(blur, vips.InterpretationBW, nil)
avg, err := vips.Avg(gray)
```

可选参数为指针字段，因此未提供的参数与显式设为零的参数可以区分。在 libvips 8.18 中，
有 357 个可选参数的默认值非零，两种情形会产生不同结果。

```go
vips.Resize(im, 0.5, &vips.ResizeOptions{Kernel: vips.Ptr(vips.KernelNearest)})
```

可选输出通过将字段指向目标变量来请求。保持为 nil 的字段不会被请求，其目标变量也不会
被写入。

```go
var x, y int
max, err := vips.Max(im, &vips.MaxOptions{X: &x, Y: &y})
```

### 通用 API

参数名即 libvips 自身的参数名，与 `vips <operation>` 输出的一致。

```go
outs, err := vips.Call("gaussblur", vips.In("in", im), vips.In("sigma", 3.0))
im, err := outs.Image("out")
```

`Out` 用于请求可选输出。`Describe`、`Operations` 与 `EnumValues` 分别报告已安装库的
操作签名、操作列表与枚举成员。

### 读取与写出

输入格式依据内容检测，输出格式由扩展名决定。

```go
im, err := vips.LoadFile("in.heic")
buf, err := vips.SaveBuffer(im, ".jpg", vips.In("Q", 90))
```

source 与 target 可直接对接 `io.Reader` 与 `io.Writer`，无需中间文件。

```go
src, _ := vips.NewSourceFromReader(req.Body)
defer src.Close()
im, _ := vips.LoadSource(src)

target, _ := vips.NewTargetToWriter(w)
defer target.Close()
err = vips.SaveTarget(im, target, ".webp", vips.In("Q", 82))
if err := target.Err(); err != nil {
    // writer 报告的错误，而非 libvips 的通用错误信息
}
```

## 语义

### 图像共享

libvips 会缓存已构建的操作，因此两次相同的调用可能返回指向同一底层图像的句柄。并发
读取是安全的；修改图像头部则不然，其变更对所有持有者可见。修改前应先复制。

```go
own, _ := vips.Copy(im, nil)
own.SetString("comment", "mine")
```

就地修改输入的操作（draw 系列）由绑定层处理：`Call` 会替换为一份私有副本并将其返回，
调用方传入的参数不会被修改。

```go
drawn, err := vips.DrawRect(im, []float64{255, 0, 0}, 60, 60, 300, 200, nil)
```

### 句柄生命周期

图像持有一个 libvips 引用，由 `Close` 或垃圾回收器释放。`Close` 之后继续使用会以
`*ClosedError` panic，而不会解引用已释放的内存。

句柄可并发使用，`Close` 亦然。每个方法在进入 C 之前获取自身的引用，因此与其他调用
竞争的 `Close` 要么发生在该调用完成之后，要么使该调用以 `*ClosedError` panic。

图像是惰性求值的流水线而非像素缓冲，因此流式 source 必须保持开启直至图像被求值。
`CopyMemory` 会将像素实体化并解除该依赖。

### 取消

libvips 不具备 deadline 机制。`CancelOn` 在下一次进度上报时终止流水线，并报告原因。

```go
w, _ := im.CancelOn(ctx)
defer w.Stop()

if _, err := vips.SaveBuffer(im, ".webp"); err != nil {
    if cause := w.Err(); cause != nil {
        return cause    // context.DeadlineExceeded，而非通用失败信息
    }
    return err
}
```

## 安全

用于解码不可信输入的进程：

```go
vips.BlockUntrusted(true)                          // 阻止 libvips 标记为不可信的加载器
vips.BlockOperation("VipsForeignLoad", true)       // 或阻止全部加载器，
vips.BlockOperation("VipsForeignLoadJpeg", false)  // 再放行特定格式
vips.SetPipeReadLimit(64 << 20)                    // 限制不可 seek 输入的缓冲上限
```

受支持的配置与漏洞报告流程见 [SECURITY.md](SECURITY.md)。

## 测试

本绑定不含逐操作代码，因此其正确性由差分测试而非代码审阅确立。`internal/difftest`
将每个操作分别通过本绑定与 `vips` 命令行执行，两侧调用由同一组参数值构造，并要求结果
完全一致。两侧均锁定单工作线程运行，因为 libvips 的若干归约操作在不同线程数下不具备
逐位可复现性。

| 目标 | 范围 |
|---|---|
| `make test` | 单元测试 |
| `make race` | 竞态检测器下的单元测试 |
| `make diff` | 与 `vips` 命令行的差分比对 |
| `make soak` | 200 轮串行与 320 轮并发下的 libvips 分配计数器 |
| `make cleak` | 对 C 核心运行 AddressSanitizer，并报告泄漏检测是否可用 |
| `make cover` | govips 与 vipsgen 暴露的每个操作在此均须可达 |

持续集成在 libvips 8.14（Debian 12 容器）、8.15（ubuntu-24.04）与 8.18（macOS）上
运行整套测试。另有独立任务针对已安装的库重新生成类型化 API，并要求生成结果可编译且
通过测试。

## 兼容性

类型化 API 由已安装的 libvips 生成，其表面随所用版本而变化；通用 API 则不受影响。
版本发布遵循语义化版本规范，并适用 1.0 之前次版本可能引入不兼容变更的约定。
`vips.PackageVersion` 报告当前发布版本，`vips.ModuleVersion` 报告模块图实际解析到的
版本，`vips.Version` 报告运行时链接的 libvips 版本。

## 对比

操作覆盖率由 `internal/coverage` 验证，该测试要求 govips 与 vipsgen 暴露的每个操作
在此均可达。

| 绑定 | 可达操作数 |
|---|---|
| vipsx | 330 |
| vipsgen | 289 |
| govips | 185 |

vipsx 与两者的区别在于：其 API 表面在运行时推导，而非按 libvips 版本分发预生成的
绑定代码。

`examples/gallery` 会针对一张照片渲染 35 个操作并生成索引页。输出已提交至
[site/](site/) 目录，该目录带有独立的 `go.mod`，以将图片排除在模块归档之外。

## 许可证

MIT，详见 [LICENSE](LICENSE)。
