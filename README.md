# pprotein MCP Server

[pprotein](https://github.com/kaz/pprotein) の Web 画面で確認できる情報を、MCP (Model Context Protocol) のリソースとツールとして提供するサーバです。ISUCON のようなチューニングで、pprof・alp・slp の結果をベンチマーク実行単位で AI エージェントに読ませ、実行間で比較させるために使います。

pprotein の内部 Web API を呼び出して情報を取得します。内部 API は公開仕様ではないため、pprotein の更新で動かなくなることがあります。

## 機能

- **Group**: 1 回のベンチマーク実行で収集されたスナップショットを、コミットハッシュと施策の記録とともに提供
- **pprof**: プロファイルを解析し、アプリケーションコードのボトルネックを提供
- **HTTP Log**: alp による HTTP アクセスログの集計結果を提供
- **Slow Log**: slp による MySQL スロークエリログの集計結果を提供

## インストール

Go 1.22 以降が必要です。

```bash
go install github.com/okashoi/pprotein-mcp-server@latest
```

pprotein が動いているサーバとビルドする環境で OS やアーキテクチャが異なる場合は、クロスコンパイルしたバイナリを配置します。

```bash
git clone https://github.com/okashoi/pprotein-mcp-server.git
cd pprotein-mcp-server
GOOS=linux GOARCH=amd64 make
scp pprotein-mcp-server <pprotein のホスト>:
```

## セットアップ

### 計測対象サーバ

pprotein の integration を組み込みます。

```go
import "github.com/kaz/pprotein/integration/echov4"

echov4.Integrate(e)
```

`/debug/pprof/*`、`/debug/log/httplog`、`/debug/log/slowlog` が追加され、レスポンスに `X-Git-Repository` ヘッダ（HEAD のハッシュ・ref・author・message・remote）が付きます。group の `commits` はこのヘッダが出どころなので、次の環境変数を揃えておきます。

| 環境変数 | デフォルト | 指すもの |
|:--|:--|:--|
| `PPROTEIN_GIT_REPOSITORY` | `.` | デプロイ先のリポジトリ |
| `PPROTEIN_HTTPLOG` | `/var/log/nginx/access.log` | alp に渡すアクセスログ（LTSV 形式） |
| `PPROTEIN_SLOWLOG` | `/var/log/mysql/mysql-slow.log` | slp に渡すスロークエリログ |

ベンチマーカが `POST /initialize` を叩いたら、収集を非同期に発火します。

```go
go func() {
	if _, err := http.Get(pproteinURL + "/api/group/collect"); err != nil {
		log.Printf("failed to trigger pprotein: %v", err)
	}
}()
```

### pprotein

targets.json（Web 画面の設定、または `POST /api/group/targets`）に計測対象を登録します。`GET /api/group/collect` は、ここに並んだ全ターゲットを同一の GroupId で収集するため、1 回のベンチマークが 1 つの group になります。

- `Duration` はベンチマークの走行時間に合わせます。短いと計測区間が足りず、長いとベンチマーク終了後のアイドル時間を含みます
- `Label` は `diff_groups` がスナップショットを対応付けるキーです。複数台構成では `app1` / `db1` のように、実行ごとに変わらない名前にします

alp.yml / slp.yml も Web 画面から設定します。`keywords` で出力列を絞る場合は、`diff_groups` が読む列を残してください。

| | 読む列 |
|:--|:--|
| alp | `Count` `Method` `Uri` `Sum` `Avg` `P99` `Max` `5xx` |
| slp | `Count` `Query` `Sum(QueryTime)` `Avg(QueryTime)` `P99(QueryTime)` `Sum(RowsExamined)` |

### MCP サーバ

```bash
PPROTEIN_URL=http://localhost:9000 PORT=9999 ./pprotein-mcp-server
```

| 環境変数 | デフォルト |
|:--|:--|
| `PPROTEIN_URL` | `http://localhost:9000` |
| `PORT` | `9999` |

pprotein のストレージを直接読まないので、pprotein に HTTP で届けばどこで動かしても構いません。MCP クライアントには Streamable HTTP で登録します。

```json
{ "servers": { "pprotein": { "type": "http", "url": "http://<pprotein のホスト>:9999/mcp" } } }
```

## 使い方

1. 施策を実装してデプロイする
2. ベンチマークを回す。`POST /initialize` が収集を発火し、pprof・httplog・slowlog が 1 つの group に入る
3. ベンチマーク終了後、`annotate_group` で施策とスコアを記録する。省略すると最新の実行に記録される
4. `diff_groups` で前回の実行やベースラインと比較する
5. 比較結果の各行に付く `uri` から該当のスナップショットを読み、ボトルネックを深掘りする

### つまずきやすいところ

- **`latest` が目的の実行とは限らない** … `/initialize` を手動で叩いた分もグループとして増えます。`pprotein://group/list` の先頭数件を時刻で確かめて、GroupId を明示してください
- **`commits` が空** … integration が組み込まれていないか、`PPROTEIN_GIT_REPOSITORY` がデプロイ先のリポジトリを指していません
- **`commits` が複数ある** … サーバ間でデプロイがずれています。どのサーバが何を申告したかは各要素の `labels` で読めます
- **`diff_groups` に `unpaired` が出る** … targets.json の `Label` が実行間で変わったか、そのスナップショットの収集・集計が失敗しています（理由が入ります）
- **`measures` が空で `memos` に JSON が見える** … `schema` が一致しない memo です。手書きの memo は `memos` にテキストとして残ります

## MCP リソース

### ベンチマーク実行単位

| URI | 内容 |
|:--|:--|
| `pprotein://group/list` | ベンチマーク実行の一覧。実行ごとのコミットハッシュと施策の記録を含む |
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

結果として記録先の実行（GroupId・時刻・コミットハッシュ）を返すので、取り違えていないかその場で確認できます。

### `diff_groups`

2 つのベンチマーク実行を比較します。引数は `base` / `target`（GroupId、または最新の実行を指す `latest`）と `limit`（各比較が返す行数、デフォルト 20）です。

- httplog は alp の集計列（`Sum` の変化で順位付け）、slowlog は slp の集計列（`Sum(QueryTime)` の変化で順位付け）、pprof は関数ごとのプロファイル占有率を比較します
- スナップショットは Label で対応付けます。対応が取れないものは `unpaired` に理由とともに並びます
- 一方の実行にしか現れないエンドポイントやクエリは、欠けている側の値が `null` になります
- 両方の実行のコミットハッシュと施策の記録を併せて返します

## 施策の記録形式

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

記録は `annotate_group` ツールか、同じことをする `scripts/annotate.sh`（curl と jq のみ）で行います。後者はベンチマーク実行のラッパや CI から叩く用です。

```bash
PPROTEIN_URL=http://pprotein:9000 ./scripts/annotate.sh \
  --score 12345 --pass true --tags db,index "reactions に複合インデックスを追加"
```

`--group` を省略すると最新の実行に記録します。

## 実装上の前提

pprotein には group の一覧 API が無いため、本サーバは種別ごとのスナップショット一覧を取得し、`GroupId` でまとめて group を組み立てます（pprotein の Web 画面と同じ方法です）。`GroupId` を持たないスナップショットは group に現れず、種別ごとのリソースからのみ辿れます。

`GET /api/group/collect` を使わず、`POST /api/{type}` に `GroupId` を自分で指定して収集しても同じようにまとまります。

## ライセンス

[MIT License](LICENSE)
