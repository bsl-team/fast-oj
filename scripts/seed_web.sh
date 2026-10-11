#!/usr/bin/env bash
# Seed a demo OJ and leave both processes running for browser inspection.
set -u
cd "$(dirname "$0")/.."
B=http://127.0.0.1:18080
taskkill //IM fast-oj.exe //F >/dev/null 2>&1
taskkill //IM fast-judge.exe //F >/dev/null 2>&1
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
stmt = """# A+B 求和

给定两个整数 $a$ 和 $b$，输出它们的和 $a+b$。

## 输入格式

一行两个整数，以空格分隔：

```
a b
```

## 输出格式

一行一个整数。参考实现：

```cpp
#include <iostream>
int main(){ int a,b; std::cin >> a >> b; std::cout << a+b; }
```

## 数据范围

| 档位 | 范围 | 分值 |
|------|------|------|
| 1 | $0 \\le a,b \\le 10$ | 40 |
| 2 | $0 \\le a,b \\le 10^9$ | 60 |

求证平均数满足：

$$ \\frac{a+b}{2} \\ge \\sqrt{ab} \\quad (a,b > 0) $$

> 提示：注意 $10^9$ 级别下 `int` 仍然够用。
"""
open(".tmp/problem.json","w",encoding="utf-8").write(json.dumps({
  "pid":"P1001","title":"A+B 求和","statement":stmt,
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

nohup ./dist/fast-oj.exe -addr 127.0.0.1:18080 -data data -admin-token ADM -judge-token JDG -v >/dev/null 2>&1 &
sleep 1.5
curl -s -X POST -H "Authorization: Bearer ADM" -H "Content-Type: application/json" --data-binary @.tmp/problem.json $B/problems >/dev/null
curl -s -X POST -H "Authorization: Bearer ADM" -H "Content-Type: application/zip" --data-binary @.tmp/cases.zip $B/problems/P1001/cases >/dev/null
curl -s -X POST -H "Authorization: Bearer ADM" -H "Content-Type: application/json" --data-binary @.tmp/user.json $B/users >/dev/null
TOK=$(curl -s -X POST -H "Authorization: Bearer ADM" -H "Content-Type: application/json" -d '{}' $B/users/alice/tokens | head -1 | sed 's/token=\([^ ]*\).*/\1/')
echo "$TOK" > .tmp/token
curl -s -X POST -H "Authorization: Bearer $TOK" -H "Content-Type: application/json" --data-binary @.tmp/ac.json $B/submissions >/dev/null
curl -s -X POST -H "Authorization: Bearer $TOK" -H "Content-Type: application/json" --data-binary @.tmp/wa.json $B/submissions >/dev/null
nohup ./dist/fast-judge.exe -server $B -token JDG -name box-local -v >/dev/null 2>&1 &
sleep 4
echo "server:  $B"
echo "token:   $TOK"
curl -s "$B/about" | grep counters
