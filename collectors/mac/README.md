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

**不要**去 `~/.opencontext/bin` 找隐藏文件；只在 **应用程序** 里添加 **OpenContext Collector**。
