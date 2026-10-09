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

### `annotate_group`

ベンチマーク実行に施策を記録します。引数は `comment`（必須）、`group`（GroupId、デフォルトは最新の実行 `latest`）、`score` / `pass` / `tags`（任意）です。pprotein の memo として投稿され、group の `measures` と `diff_groups` の出力に乗ります。

結果として記録先の実行（GroupId・時刻・コミットハッシュ）を返すので、取り違えていないかその場で確認できます。`/initialize` から収集をトリガしている場合、グループの時刻はベンチ開始時刻になるため、ベンチ終了直後なら `latest` が目的の実行と一致します。`/initialize` を複数回叩いた場合は最新が目的の実行とは限らないので、`pprotein://group/list` で確認してから GroupId を明示してください。

ベンチマーク対象のコミットハッシュは渡しません。計測対象サーバが申告したものを pprotein が記録しています。

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

### 施策の記録形式

pprotein の memo は自由文なので、施策は次の JSON を memo の本文として記録します。`schema` が一致する memo だけが group の `measures` に構造化されて並び、それ以外の memo は `memos` にテキストとして残ります。

```json
{
  "schema": "pprotein-measure/1",
  "comment": "reactions に複合インデックスを追加",
  "score": 12345,
  "pass": true,
  "tags": ["db", "index"]
}
```

ベンチマーク対象のコミットはこの JSON に含めません。計測対象サーバが申告したものを pprotein が記録しており、group の `commits` で読めます。
