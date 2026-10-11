#!/usr/bin/env bash
# Two-process smoke test: starts dist/fast-oj.exe + dist/fast-judge.exe, seeds a
# problem over curl, submits an AC and a WA program, prints the verdicts.
set -u
cd "$(dirname "$0")/.."
B=http://127.0.0.1:18090
rm -rf data && mkdir -p .tmp

cat > .tmp/ac.cpp <<'CPP'
#include <iostream>
int main(){int a,b;std::cin>>a>>b;std::cout<<a+b<<std::endl;return 0;}
CPP
cat > .tmp/wa.cpp <<'CPP'
#include <iostream>
int main(){int a,b;std::cin>>a>>b;std::cout<<a*b<<std::endl;return 0;}
CPP

python - <<'PY'
import json, zipfile
open(".tmp/problem.json","w",encoding="utf-8").write(json.dumps({
  "pid":"P1001","title":"A+B 求和",
  "statement":"输入两个整数 a 和 b，输出它们的和。\n样例见下。",
  "time_limit_ms":1000,"mem_limit_kb":262144,"languages":["c","cpp"]}, ensure_ascii=False))
with zipfile.ZipFile(".tmp/cases.zip","w") as z:
    z.writestr("sample1.in","1 2\n"); z.writestr("sample1.out","3\n")
    z.writestr("1.in","3 4\n");      z.writestr("1.out","7\n")
    z.writestr("2.in","100 200\n");  z.writestr("2.out","300\n")
open(".tmp/user.json","w").write(json.dumps({"handle":"alice","role":"user"}))
for name, src in (("ac",".tmp/ac.cpp"), ("wa",".tmp/wa.cpp")):
    open(".tmp/%s.json" % name,"w",encoding="utf-8").write(json.dumps(
        {"problem":"P1001","language":"cpp","source":open(src,encoding="utf-8").read()}))
print("fixtures ready")
PY

./dist/fast-oj.exe -addr 127.0.0.1:18090 -data data -admin-token ADM -judge-token JDG &
SRV=$!
sleep 1.5
./dist/fast-judge.exe -server $B -token JDG -name box-local -v &
JDG=$!
sleep 0.5

curl -s -X POST -H "Authorization: Bearer ADM" -H "Content-Type: application/json" --data-binary @.tmp/problem.json $B/problems
curl -s -X POST -H "Authorization: Bearer ADM" --data-binary @.tmp/cases.zip $B/problems/P1001/cases
curl -s -X POST -H "Authorization: Bearer ADM" -H "Content-Type: application/json" --data-binary @.tmp/user.json $B/users
TOK=$(curl -s -X POST -H "Authorization: Bearer ADM" -H "Content-Type: application/json" -d '{}' $B/users/alice/tokens | head -1 | sed 's/token=\([^ ]*\).*/\1/')
echo "alice token: ${TOK:0:8}..."

echo "--- submit correct program ---"
curl -s -X POST -H "Authorization: Bearer $TOK" -H "Content-Type: application/json" --data-binary @.tmp/ac.json $B/submissions
echo "--- wait for verdict ---"
curl -s -H "Authorization: Bearer $TOK" "$B/submissions/1?wait=25"

echo "--- submit wrong program ---"
curl -s -X POST -H "Authorization: Bearer $TOK" -H "Content-Type: application/json" --data-binary @.tmp/wa.json $B/submissions
echo "--- wait for verdict ---"
curl -s -H "Authorization: Bearer $TOK" "$B/submissions/2?wait=25"

echo "--- list mine ---"
curl -s -H "Authorization: Bearer $TOK" "$B/submissions?user=me"
echo "--- problem detail ---"
curl -s "$B/problems/P1001" | head -12
echo "--- judge boxes ---"
curl -s -H "Authorization: Bearer ADM" $B/judges
echo "--- about ---"
curl -s $B/about | grep counters

kill $JDG $SRV 2>/dev/null
sleep 0.5
taskkill //IM fast-judge.exe //F >/dev/null 2>&1
taskkill //IM fast-oj.exe //F >/dev/null 2>&1
echo "[smoke done]"
