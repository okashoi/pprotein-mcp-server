# pprotein MCP Server

Go 言語で実装された pprotein 用 MCP (Model Context Protocol) サーバです。pprotein Web API と連携し、HTTP ログの統計情報とスローログの統計情報を MCP リソースとして提供します。

## 機能

- **HTTP Log Statistics**: HTTP アクセスログ統計情報を提供
- **Slow Log Statistics**: MySQL スロークエリログ統計情報を提供
- **Latest File Discovery**: データディレクトリ内の最新ログファイルを自動発見

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

- `DATA_DIR`: pprotein のデータディレクトリパス (デフォルト: `./data`)
- `PPROTEIN_URL`: pprotein サーバの URL (デフォルト: `http://localhost:9000`)
- `PORT`: MCP サーバのポート番号 (デフォルト: `9999`)

## MCP リソース

- `pprotein://httplog` - HTTP アクセスログ統計情報
- `pprotein://slowlog` - MySQL スロークエリログ統計情報
