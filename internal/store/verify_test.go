package store

import (
	"context"
	"testing"
	"time"
)

func TestVerifyChallengeWaitsAndIsSingleUse(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	token, readyAt, err := db.NewVerifyChallenge(ctx, VerifyRegister, "203.0.113.9", 60*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if readyAt <= nowMS() {
		t.Fatalf("readyAt = %d，已经是可以直接放行的时刻", readyAt)
	}

	// 时间没到：这是这张表唯一的作用，浏览器把等待说成多久都不算数。
	if ok, err := db.SpendVerifyChallenge(ctx, token, VerifyRegister, "203.0.113.9"); err != nil || ok {
		t.Fatalf("没到时间就核销成功 = %v（err %v）", ok, err)
	}
	// 换用途、换来源地址都不算：登录页领的令牌交不了注册表单，别处的地址也用不了它。
	time.Sleep(80 * time.Millisecond)
	if ok, err := db.SpendVerifyChallenge(ctx, token, VerifyLogin, "203.0.113.9"); err != nil || ok {
		t.Fatalf("换用途核销成功 = %v（err %v）", ok, err)
	}
	if ok, err := db.SpendVerifyChallenge(ctx, token, VerifyRegister, "198.51.100.7"); err != nil || ok {
		t.Fatalf("换来源地址核销成功 = %v（err %v）", ok, err)
	}
	if ok, err := db.SpendVerifyChallenge(ctx, "", VerifyRegister, "203.0.113.9"); err != nil || ok {
		t.Fatalf("空令牌核销成功 = %v（err %v）", ok, err)
	}
	if ok, err := db.SpendVerifyChallenge(ctx, token, VerifyRegister, "203.0.113.9"); err != nil || !ok {
		t.Fatalf("到时间后核销 = %v（err %v）", ok, err)
	}
	// 用过即废：同一个令牌提交两次，第二次必须算没过。
	if ok, err := db.SpendVerifyChallenge(ctx, token, VerifyRegister, "203.0.113.9"); err != nil || ok {
		t.Fatalf("令牌被用了第二次 = %v（err %v）", ok, err)
	}
}

func TestVerifyChallengeKeepsOneWindowPerAddress(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	first, _, err := db.NewVerifyChallenge(ctx, VerifyRegister, "203.0.113.10", time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	// 刷新一次注册页就该只剩一个待核销窗口，否则匿名刷页能无限制往表里堆行。
	second, _, err := db.NewVerifyChallenge(ctx, VerifyRegister, "203.0.113.10", time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("两次发令牌给出同一个值")
	}
	time.Sleep(20 * time.Millisecond)
	if ok, _ := db.SpendVerifyChallenge(ctx, first, VerifyRegister, "203.0.113.10"); ok {
		t.Error("旧的令牌还能核销：刷页留下的行没被收掉")
	}
	if ok, _ := db.SpendVerifyChallenge(ctx, second, VerifyRegister, "203.0.113.10"); !ok {
		t.Error("最新那个令牌核销不掉")
	}
}

func TestVerifyChallengeExpires(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	token, _, err := db.NewVerifyChallenge(ctx, VerifyLogin, "203.0.113.11", time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	// 领了不用就作废：不然一个地址攒着令牌，哪天想提交就随时能提交。
	if _, err := db.SQL.ExecContext(ctx,
		`UPDATE verify_challenges SET expires_at=? WHERE token=?`, nowMS()-1, token); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if ok, err := db.SpendVerifyChallenge(ctx, token, VerifyLogin, "203.0.113.11"); err != nil || ok {
		t.Fatalf("过期令牌仍然可用 = %v（err %v）", ok, err)
	}
}

func TestRateHitsCountsInsideWindowOnly(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	const key = "verify-count"
	if n, err := db.RateHits(ctx, key, time.Hour); err != nil || n != 0 {
		t.Fatalf("没有记录时 RateHits = %d（err %v）", n, err)
	}
	for want := 1; want <= 3; want++ {
		n, err := db.RateHits(ctx, key, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		if n != want-1 {
			t.Fatalf("第 %d 次查 = %d，应该是 %d", want, n, want-1)
		}
		got, err := db.RegisterLoginFailure(ctx, key, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("RegisterLoginFailure 返回 %d，应该是 %d", got, want)
		}
	}
	if n, err := db.RateHits(ctx, key, time.Hour); err != nil || n != 3 {
		t.Fatalf("三次之后 RateHits = %d（err %v）", n, err)
	}
	// 窗口已经过去的那些不算了——LoginAllowed 也是这么判的，两处口径必须一致，
	// 否则渲染出来的等待秒数会跟着一个早已清零的计数涨。窗口是 1 毫秒，那几笔写在
	// 同一毫秒里，所以要真过一会儿再问。
	time.Sleep(20 * time.Millisecond)
	if n, err := db.RateHits(ctx, key, time.Millisecond); err != nil || n != 0 {
		t.Fatalf("窗口过期后 RateHits = %d（err %v）", n, err)
	}
}
