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

	// 0029 新增的那一条：时间到了但没点过，照样核销不掉。少了这一步，"睡够秒数直接交表单"
	// 就是完全合法的路线，复选框成了纯装饰。
	if ok, err := db.SpendVerifyChallenge(ctx, token, VerifyRegister, "203.0.113.9"); err != nil || ok {
		t.Fatalf("没点过复选框也核销成功 = %v（err %v）", ok, err)
	}
	// 盖章同样认来源地址和用途。
	if ok, err := db.MarkVerifyClick(ctx, token, VerifyLogin, "203.0.113.9"); err != nil || ok {
		t.Fatalf("换个用途就盖上章 = %v（err %v）", ok, err)
	}
	if ok, err := db.MarkVerifyClick(ctx, token, VerifyRegister, "198.51.100.7"); err != nil || ok {
		t.Fatalf("换个来源地址就盖上章 = %v（err %v）", ok, err)
	}
	if ok, err := db.MarkVerifyClick(ctx, token, VerifyRegister, "203.0.113.9"); err != nil || !ok {
		t.Fatalf("正常盖章 = %v（err %v）", ok, err)
	}

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
		t.Fatalf("点过章且到时间后核销 = %v（err %v）", ok, err)
	}
	// 用过即废：同一个令牌提交两次，第二次必须算没过。
	if ok, err := db.SpendVerifyChallenge(ctx, token, VerifyRegister, "203.0.113.9"); err != nil || ok {
		t.Fatalf("令牌被用了第二次 = %v（err %v）", ok, err)
	}
}

func TestMarkVerifyClickWaitsForTheClock(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	token, _, err := db.NewVerifyChallenge(ctx, VerifyRegister, "203.0.113.13", 40*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	// 抢着点不算：盖章也要等 ready_at，否则"手比脑子快"等于跳过等待。
	if ok, err := db.MarkVerifyClick(ctx, token, VerifyRegister, "203.0.113.13"); err != nil || ok {
		t.Fatalf("等待没走完就盖上章 = %v（err %v）", ok, err)
	}
	time.Sleep(60 * time.Millisecond)
	if ok, err := db.MarkVerifyClick(ctx, token, VerifyRegister, "203.0.113.13"); err != nil || !ok {
		t.Fatalf("到时间后盖章 = %v（err %v）", ok, err)
	}
	// 盖过章的窗口要能在渲染时被认出来，不然页面会挡着一个已经完成的验证。
	_, at, verified, pending, err := db.PendingVerifyChallenge(ctx, VerifyRegister, "203.0.113.13")
	if err != nil || !pending || !verified || at == 0 {
		t.Fatalf("找回已盖章的窗口 = %d/盖过%v/%v（err %v）", at, verified, pending, err)
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
	if ok, err := db.MarkVerifyClick(ctx, second, VerifyRegister, "203.0.113.10"); err != nil || !ok {
		t.Fatalf("给最新那个窗口盖章 = %v（err %v）", ok, err)
	}
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
	if ok, err := db.MarkVerifyClick(ctx, token, VerifyLogin, "203.0.113.11"); err != nil || ok {
		t.Fatalf("过期令牌仍然盖得上章 = %v（err %v）", ok, err)
	}
	if ok, err := db.SpendVerifyChallenge(ctx, token, VerifyLogin, "203.0.113.11"); err != nil || ok {
		t.Fatalf("过期令牌仍然可用 = %v（err %v）", ok, err)
	}
}

func TestPendingVerifyChallengeFindsTheOngoingWindow(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	// 还没有人领过窗口。
	if _, _, _, pending, err := db.PendingVerifyChallenge(ctx, VerifyRegister, "203.0.113.12"); err != nil || pending {
		t.Fatalf("空表就有待核销窗口 = %v（err %v）", pending, err)
	}
	token, readyAt, err := db.NewVerifyChallenge(ctx, VerifyRegister, "203.0.113.12", 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	got, at, verified, pending, err := db.PendingVerifyChallenge(ctx, VerifyRegister, "203.0.113.12")
	if err != nil || !pending || got != token || at != readyAt || verified {
		t.Fatalf("找回的窗口 = %q/%d/盖过%v/%v（err %v），应该是 %q/%d/false/true", got, at, verified, pending, err, token, readyAt)
	}
	// 用途和来源地址都得对上，别处的、别的用途的窗口不算。
	if _, _, _, pending, _ := db.PendingVerifyChallenge(ctx, VerifyLogin, "203.0.113.12"); pending {
		t.Error("换个用途也找回了同一个窗口")
	}
	if _, _, _, pending, _ := db.PendingVerifyChallenge(ctx, VerifyRegister, "198.51.100.8"); pending {
		t.Error("换个来源地址也找回了同一个窗口")
	}
	// 等够、盖章、核销掉，就没有"还在计时"这一说了。
	time.Sleep(40 * time.Millisecond)
	if ok, err := db.MarkVerifyClick(ctx, token, VerifyRegister, "203.0.113.12"); err != nil || !ok {
		t.Fatalf("盖章 = %v（err %v）", ok, err)
	}
	if ok, err := db.SpendVerifyChallenge(ctx, token, VerifyRegister, "203.0.113.12"); err != nil || !ok {
		t.Fatalf("核销 = %v（err %v）", ok, err)
	}
	if _, _, _, pending, _ := db.PendingVerifyChallenge(ctx, VerifyRegister, "203.0.113.12"); pending {
		t.Error("已经用掉的窗口还算在计时")
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
