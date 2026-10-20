#!/bin/bash
# 更新模板中静态资源的版本号（内容 hash），避免浏览器缓存旧 CSS/JS
set -e
cd "$(dirname "$0")/.."

css=$(md5sum src/static/app.css | cut -c1-8)
js=$(md5sum src/static/app.js | cut -c1-8)
qr=$(md5sum src/static/qrcode.min.js | cut -c1-8)

for f in src/views/*.html; do
  sed -i -E "s#/static/app\.css(\?v=[a-f0-9]+)?#/static/app.css?v=$css#g" "$f"
  sed -i -E "s#/static/app\.js(\?v=[a-f0-9]+)?#/static/app.js?v=$js#g" "$f"
  sed -i -E "s#/static/qrcode\.min\.js(\?v=[a-f0-9]+)?#/static/qrcode.min.js?v=$qr#g" "$f"
done

echo "静态资源版本已更新: css=$css js=$js qrcode=$qr"
