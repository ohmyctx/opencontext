# OpenContext macOS Collector

## 安装

```bash
bash install.sh
```

会在 **应用程序** 里安装（可见、可勾选）：

```text
~/Applications/OpenContext Collector.app
```

## 授权

```bash
bash grant-accessibility.sh
```

1. 先 **打开 Finder**（定位到 OpenContext Collector）
2. 再 **打开系统设置 → 辅助功能**
3. 点 **+** → 左侧 **应用程序** → 选 **OpenContext Collector**  
   （若看不到：点 + 后按 **⌘⇧G**，路径已复制到剪贴板，⌘V 粘贴）

验证：

```bash
bash run.sh --check-permissions
```

## 运行

```bash
bash run.sh
```

## 配置

推荐配置文件位置：

```text
~/.config/opencontext/collectors/macos.yaml
```

旧路径仍兼容：

```text
~/.opencontext/mac-collector.yaml
```

截图采集是 L3，默认关闭。事件只上报本地图片路径，不上传图片内容：

```yaml
collect_screenshots: false
screenshot_interval_secs: 300
screenshot_dir: "~/Library/Application Support/OpenContext/screenshots/macos"
screenshot_max_width: 1440
screenshot_format: "jpg"
screenshot_retention_days: 3
screenshot_max_total_mb: 1024
```

开启截图后，macOS 还需要给 **OpenContext Collector** 授予 Screen Recording
权限；这是独立于 Accessibility 的权限。

**不要**去 `~/.opencontext/bin` 找隐藏文件；只在 **应用程序** 里添加 **OpenContext Collector**。
