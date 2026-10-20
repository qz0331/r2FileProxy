# Release 产物说明

此目录用于存放打包后的二进制，**不纳入版本库**。

## 生成

```bash
./build.sh            # Linux/macOS
build.bat             # Windows
```

构建会产出：

```
release/
├── windows/
│   ├── r2proxy.exe
│   ├── views/          # HTML 模板
│   ├── static/         # app.css / app.js / qrcode.min.js
│   └── .env.example
└── linux/
    ├── r2proxy
    ├── views/
    ├── static/
    └── .env.example
```

## 运行

```bash
cd release/linux
cp .env.example .env     # 填写 R2 配置与 SERVER_SECRET
chmod +x r2proxy
./r2proxy
```

> 需要通过反向代理暴露时，请设置 `TRUSTED_PROXIES` 并开启 `SECURE_COOKIE=true`。
