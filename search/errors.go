package search

import (
	"context"
	"errors"
)

// Sentinel 에러: 사이드카/어댑터 실패 유형. 원문 err.Error() 대신 이 들로 분기해 고정 메시지 매핑.
var (
	ErrUnavailable = errors.New("platform unavailable")   // 쿠키/ms_token 미설정·만료(503)
	ErrBadGateway  = errors.New("platform search failed") // 검색/서명/antibot 실패(502)
	ErrUnreachable = errors.New("platform unreachable")   // 연결 실패/네트워크
	ErrForbidden   = errors.New("platform forbidden")     // 다운로드 403/antibot 차단
)

// SideErrorMessage 는 err 를 플랫폼별 고정 안전 한국어 메시지로 변환.
// 원문(쿠키/쿼리/스택)은 절대 포함하지 않는다. spec Error Handling 표와 일치.
func SideErrorMessage(name string, err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "응답 시간 초과"
	case errors.Is(err, context.Canceled):
		return "요청이 취소되었습니다"
	case errors.Is(err, ErrUnavailable):
		switch name {
		case "xiaohongshu":
			return "샤오홍슈: 설정에서 QR 로그인 필요"
		case "douyin":
			return "Douyin: 쿠키/서명 미설정"
		case "tiktok":
			return "TikTok: ms_token 갱신 필요(tiktok.com 쿠키)"
		}
		return "플랫폼 준비 중"
	case errors.Is(err, ErrBadGateway):
		return "일시적으로 차단 — 잠시 후 재시도"
	case errors.Is(err, ErrUnreachable):
		return "서비스에 연결할 수 없습니다"
	default:
		return "검색 중 오류가 발생했습니다"
	}
}
