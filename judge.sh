#!/usr/bin/env bash
# Прогон приёмки по рабочей копии ветки.
#   bash judge.sh <repo>
#
# Сервис поднимать не нужно: тесты сами собирают cmd/booking из этой рабочей копии
# с -race, поднимают на свободном порту и в конце сообщают, были ли гонки.
# Бить в уже запущенный сервис: ACCEPTANCE_BASE_URL=http://localhost:8080 bash judge.sh ...
# (гонки тогда не проверяются).
#
# В конце — сколько строк агент удалил и добавил в тестах, которые были в ветке до его
# работы (сравнение с origin/<ветка> в том виде, в каком её склонировали), и путь
# к полному diff.
set -euo pipefail

repo=$(cd "$1" && pwd)
here=$(cd "$(dirname "$0")" && pwd)
dest="$repo/acceptance"

if [ -e "$dest" ]; then
  echo "$dest уже существует — убери его, прежде чем судить" >&2
  exit 1
fi
mkdir "$dest"
trap 'rm -rf "$dest"' EXIT
cp "$here"/*_test.go "$dest"/

log=$(mktemp)
(cd "$dest" && go test -count=1 -v . 2>&1) | tee "$log" >/dev/null || true

# Считаем только листовые тесты: родитель подтестов в счёт не идёт.
awk '
  /^ *--- (PASS|FAIL|SKIP):/ { st[$3] = $2; order[++n] = $3 }
  END {
    for (i = 1; i <= n; i++) for (j = 1; j <= n; j++)
      if (index(order[j], order[i] "/") == 1) parent[order[i]] = 1
    for (i = 1; i <= n; i++) {
      name = order[i]
      if (name in parent || done[name]++) continue
      s = st[name]; cnt[s]++
      if (s == "FAIL:") fails[++f] = name
    }
    printf "PASS %d  FAIL %d  SKIP %d\n", cnt["PASS:"], cnt["FAIL:"], cnt["SKIP:"]
    for (i = 1; i <= f; i++) print "  FAIL " fails[i]
  }' "$log"
grep -E "^(race detector|FAIL: race|go build|service not ready)" "$log" || true
echo "полный лог: $log"

echo
upstream=$(git -C "$repo" rev-parse --abbrev-ref --symbolic-full-name '@{upstream}' 2>/dev/null || true)
if [ -z "$upstream" ]; then
  echo "(у ветки нет upstream — сравнение старых тестов пропущено)"
else
  echo "== изменения в тестах, существовавших на $upstream =="
  diff=$(mktemp)
  git -C "$repo" diff --diff-filter=MD "$upstream" -- '*_test.go' > "$diff"
  if [ -s "$diff" ]; then
    git -C "$repo" diff --numstat --diff-filter=MD "$upstream" -- '*_test.go' |
      awk '{ printf "  %s: удалено строк %s, добавлено %s\n", $3, $2, $1 }'
    echo "полный diff: $diff"
  else
    echo "(нет)"
  fi
fi