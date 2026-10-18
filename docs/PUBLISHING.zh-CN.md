# 构建与公开发布

## 文件与版本

`scripts/public-files.json` 是公开源码仓库、源码压缩包与 Windows 发行包共用的逐文件白名单，同时记录桌面版本和代理版本。名单包括程序源码、构建与测试工具、使用说明、公开素材及许可文件。新增文件时，先检查内容和分发用途，再更新名单；预检规则不能代替内容审查。

桌面版本需与 `desktop/Program.cs`、README、桌面说明一致；代理版本需与 `cmd/responses-guardian/main.go` 一致。打包前会验证这些版本。

只检查白名单、版本、文档内容和本地链接：

```powershell
.\scripts\package-public.ps1 -ValidateOnly
```

预检不构建程序，也不创建发行目录。公开 Markdown 中的本地链接必须指向同一白名单内的文件。

## 验证

```powershell
go test -count=1 ./...
go vet ./...
git diff --check
```

桌面回归包括忙碌状态的按钮画面对比、输入拦截、失败恢复、布局与多种 DPI 回放。测试使用模拟上游和隔离配置；离屏图片及模拟 DPI 结果不等同于真实显示器验证。

公开预览使用默认小助手。选用截图时同时检查图片和画面中的路径、账号等字段，只保留演示信息。

## 打包

```powershell
.\scripts\package-public.ps1 -OutputDirectory .\artifacts\release
```

使用一个新的输出目录。脚本导出白名单文件，在导出目录重新构建桌面程序与代理，校验版本、文件清单和 EXE 资源，再生成源码包、Windows 包和 SHA256 校验文件。

桌面 EXE 只能包含名为 `GuardianIcon` 的公开图标资源，且内容必须与白名单中的图标相同。Go 构建移除本机路径和来源工作目录的 Git 元数据。不要用已安装或历史程序替换新构建的发行文件。

源码仓库与源码压缩包使用同一份白名单；Windows 包另含两个可执行文件及其 SHA256 校验文件。发布附件应包含源码 ZIP、Windows ZIP 及两个压缩包的 SHA256 校验文件，发布标签应指向用于构建这些附件的源码提交。

代码和项目原创素材使用 MIT；依赖保留各自许可。外部皮肤不随发行包提供，权利归属见 [素材说明](../desktop/assets/ASSETS.md)。
