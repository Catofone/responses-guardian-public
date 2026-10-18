# 桌面素材

应用图标来自本项目原创的 `guardian-sky.svg`，PNG 与多尺寸 ICO 由 `desktop/build-icon.ps1` 生成。默认小助手由 `ModernControls.cs` 中的绘图代码绘制。上述图标、默认图案和代码使用项目 MIT 许可证。

桌面程序只嵌入应用图标，不嵌入用户的个人图片。可选的个人皮肤从本机 `%LOCALAPPDATA%\ResponsesGuardian\skins\companion.png` 读取；缺失或无法解码时使用默认图案。

外部皮肤不随源码或发行包提供，其权利由提供者自行管理，项目许可证不为外部图片授予额外权利。

发布用截图应使用 `Guardian.exe --public-preview`，或使用以默认皮肤构造窗口的测试预览。公开预览会跳过本机皮肤，独立于普通管理窗口运行。
