# git-worktreeinclude

`git worktree` で作成した作業ツリーに、`.worktreeinclude` で宣言した ignored ファイルだけを安全に適用するツールです。

## Quickstart

### Build

```sh
go build -o git-worktreeinclude ./cmd/git-worktreeinclude
```

`git worktreeinclude ...` 形式で使う場合は、`git-worktreeinclude` を `PATH` に置いてください。

### Apply

```sh
git-worktreeinclude apply --from auto
```

または Git 拡張として:

```sh
git worktreeinclude apply --from auto
```

サブコマンド省略時は `apply` と同義です。

```sh
git-worktreeinclude --from auto
```

## `.worktreeinclude` の仕様

- リポジトリルートに配置
- フォーマットは gitignore 互換（`#` コメント、空行、`!` 否定、`/` アンカー、`**` など）
- 実際の同期対象は次の積集合
  - `.worktreeinclude` にマッチするパス
  - Git が ignored と判定するパス

つまり、tracked ファイルは `.worktreeinclude` に書いても同期対象になりません。

例:

```gitignore
.env
.env.*
!.env.example

.vscode/settings.json
.idea/
```

## コマンド

### `git-worktreeinclude apply`

現在の worktree を同期先にして、source worktree からコピーします。

```sh
git-worktreeinclude apply [--from auto|<path>] [--include <path>] [--dry-run] [--force] [--json] [--quiet] [--verbose]
```

- `--from`: `auto`（デフォルト）は main worktree を自動選択
- `--include`: include ファイル（デフォルト `.worktreeinclude`）
- `--dry-run`: 実際には変更せず計画のみ
- `--force`: 差分があっても上書き
- `--json`: stdout に単一 JSON を出力
- `--quiet`: 人間向けログを抑制
- `--verbose`: 詳細表示

安全デフォルト:

- tracked は触らない
- 削除しない
- 上書きしない（差分は conflict、exit code `3`）
- `.worktreeinclude` 不在は no-op success（exit code `0`）

### `git-worktreeinclude doctor`

診断専用。dry-run 相当で要約を表示します。

```sh
git-worktreeinclude doctor [--from auto|<path>] [--include <path>] [--quiet] [--verbose]
```

表示内容:

- target repo root
- source 選択結果
- include ファイルの有無・パターン数
- matched / copy planned / conflicts / missing source / skipped same / errors

### `git-worktreeinclude hook path`

`core.hooksPath` を考慮した hooks パスを表示します。

```sh
git-worktreeinclude hook path [--absolute]
```

### `git-worktreeinclude hook print post-checkout`

推奨 `post-checkout` スニペットを出力します。

```sh
git-worktreeinclude hook print post-checkout
```

## JSON 出力

`apply --json` は stdout に単一 JSON を出力します。

```json
{
  "from": "/abs/path/source",
  "to": "/abs/path/target",
  "include_file": ".worktreeinclude",
  "summary": {
    "matched": 12,
    "copied": 8,
    "skipped_same": 3,
    "skipped_missing_src": 1,
    "conflicts": 0,
    "errors": 0
  },
  "actions": [
    {"op": "copy", "path": ".env", "status": "done"},
    {"op": "skip", "path": ".vscode/settings.json", "status": "same"},
    {"op": "conflict", "path": ".env.local", "status": "diff"}
  ]
}
```

- `path` は repo root 相対（`/` 区切り）
- ファイル内容や秘密情報は出力しません

## 外部ツール統合

worktree 作成直後に次の 1 行を実行するだけで統合できます。

```sh
git worktree add <path> -b <branch>
git -C <path> worktreeinclude apply --from auto --json
```

- 成功判定は exit code
- 詳細は JSON の `summary` / `actions`

## post-checkout 自動適用（手動導入）

自動インストールは行いません。README 手順で導入します。

### 共有 hooks（推奨）

```sh
mkdir -p .githooks
git-worktreeinclude hook print post-checkout > .githooks/post-checkout
chmod +x .githooks/post-checkout
git config core.hooksPath .githooks
```

生成される `post-checkout` の中身:

```sh
#!/bin/sh
set -eu

old="$1"
if [ "$old" = "0000000000000000000000000000000000000000" ]; then
  git worktreeinclude apply --from auto --quiet || true
fi
```

- worktree 作成直後相当（`old` が 40 ゼロ）のみ自動適用
- checkout 体験を壊さないよう、失敗は非致命

### 既存フック管理（husky 等）がある場合

- 既存 `post-checkout` を上書きせず、上の `if` ブロックのみ追記してください。
- 既に `core.hooksPath` を使っている場合はその運用に合わせて追加してください。

補足:

- `git worktree add --no-checkout` では `post-checkout` が走らない可能性があります。
- その場合は手動で `git worktreeinclude apply --from auto` を実行してください。

## Exit codes

- `0`: 成功
- `1`: 内部エラー
- `2`: 引数エラー
- `3`: conflict（`apply`）
- `4`: 環境/前提エラー

## トラブルシュート

- `not inside a git repository`: Git 管理下で実行しているか確認
- `source and target are not from the same repository`: `--from` が別 repo を指していないか確認
- conflict で止まる: `--force` を使うか、対象ファイルの差分を解消して再実行
- include 不在で何も起きない: `.worktreeinclude` 配置場所（repo root）と `--include` パスを確認

## 開発

```sh
GOCACHE=$(pwd)/.gocache go test ./...
```
