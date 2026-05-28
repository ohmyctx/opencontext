# OpenContext npm package

This package installs the `oc` binary from OpenContext GitHub Releases. Release archives also include the Chrome extension collector under `collectors/browser/chrome`, so `oc collector browser-chrome install` can prepare it after npm installation.

```bash
npm install -g @ohmyctx/opencontext
oc --version
oc daemon
```

OpenContext release assets must be named:

- `oc-v<version>-linux-amd64.tar.gz`
- `oc-v<version>-linux-arm64.tar.gz`
- `oc-v<version>-darwin-amd64.tar.gz`
- `oc-v<version>-darwin-arm64.tar.gz`
- `oc-v<version>-windows-amd64.zip`
- `oc-v<version>-windows-arm64.zip`
