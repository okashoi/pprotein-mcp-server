# pprotein MCP Server

Go 言語で実装された pprotein 用 MCP (Model Context Protocol) サーバです。pprotein の内部 Web API を呼び出し、Web 画面で確認できる情報を MCP リソースとして提供します。

## 機能

- **Group**: 1 回のベンチマーク実行で収集されたスナップショットを、コミットハッシュとメモとともに提供
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

### ベンチマーク実行単位

| URI | 内容 |
|:--|:--|
| `pprotein://group/list` | ベンチマーク実行の一覧。実行ごとのコミットハッシュとメモを含む |
| `pprotein://group/latest` | 最新のベンチマーク実行 |
| `pprotein://group/{id}` | GroupId で指定したベンチマーク実行 |

### スナップショット単位

`{type}` は `pprof` / `httplog` / `slowlog` です。

| URI | 内容 |
|:--|:--|
| `pprotein://{type}/list` | スナップショットの一覧 |
| `pprotein://{type}/latest` | 最新のスナップショット |
| `pprotein://{type}/{id}` | ID で指定したスナップショット |

`{id}` には pprotein がスナップショットに付けた ID（`m8x7kqc3f-httplog.log` のような形式）を渡します。一覧リソースで取得できます。

`httplog` と `slowlog` のスナップショットは alp / slp の集計結果（TSV）、`pprof` は解析結果（JSON）です。

## MCP ツール

### `diff_groups`

2 つのベンチマーク実行を比較します。引数は `base` / `target`（GroupId、または最新の実行を指す `latest`）と `limit`（各比較が返す行数、デフォルト 20）です。

- httplog は alp の集計列（`Sum` の変化で順位付け）、slowlog は slp の集計列（`Sum(QueryTime)` の変化で順位付け）、pprof は関数ごとのプロファイル占有率を比較します
- スナップショットは Label で対応付けます。対応が取れないものは `unpaired` に理由とともに並びます
- 一方の実行にしか現れないエンドポイントやクエリは、欠けている側の値が `null` になります
- 両方の実行のコミットハッシュとメモを併せて返します

## プロファイル結果と施策の紐づけ

pprotein には group の一覧 API が無いため、本サーバは種別ごとのスナップショット一覧を GroupId でまとめて group を組み立てます。1 回のベンチマーク実行を 1 つの group にするには、次のいずれかで収集します。

- `GET /api/group/collect` を叩く。pprotein の targets.json に登録された全ターゲットが同一の GroupId で収集される
- `POST /api/{type}` に `GroupId` を自分で指定して収集する

コミットハッシュは、計測対象サーバが `X-Git-Repository` ヘッダで申告した HEAD です。pprotein の integration を組み込み、`PPROTEIN_GIT_REPOSITORY` をデプロイ先のリポジトリに向けておく必要があります。サーバごとに申告が異なる場合は、group の `commits` に申告元の Label とともに並びます。

施策のコメントは `POST /api/memo` に `GroupId` と `Text` を渡すと group の `memos` に並びます。
