# pprotein MCP Server

Go 言語で実装された pprotein 用 MCP (Model Context Protocol) サーバです。pprotein の内部 Web API を呼び出し、Web 画面で確認できる情報を MCP リソースとして提供します。

## 機能

- **pprof**: プロファイルを解析し、アプリケーションコードのボトルネックを提供
- **HTTP Log**: alp による HTTP アクセスログの集計結果を提供
- **Slow Log**: slp による MySQL スロークエリログの集計結果を提供

## インストールと使用方法

### ビルド

```bash
make
```

### 実行

```bash
./pprotein-mcp-server
```

### 環境変数

- `PPROTEIN_URL`: pprotein サーバの URL (デフォルト: `http://localhost:9000`)
- `PORT`: MCP サーバのポート番号 (デフォルト: `9999`)

## MCP リソース

`{type}` は `pprof` / `httplog` / `slowlog` です。

| URI | 内容 |
|:--|:--|
| `pprotein://{type}/list` | スナップショットの一覧 |
| `pprotein://{type}/latest` | 最新のスナップショット |
| `pprotein://{type}/{id}` | ID で指定したスナップショット |

`{id}` には pprotein がスナップショットに付けた ID（`m8x7kqc3f-httplog.log` のような形式）を渡します。一覧リソースで取得できます。
