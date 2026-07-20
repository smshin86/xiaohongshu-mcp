package errors

import "errors"

var ErrNoFeeds = errors.New("没有捕获到 feeds 数据")
var ErrNoFeedDetail = errors.New("没有捕获到 feed 详情数据")

// ErrAuthLost: 검색 페이지 이동 등 XHS 위험제어로 세션이 무효화된 경우.
// 60s timeout 대신 빠르게 fast-fail 하기 위한 명시적 에러. SearchFeeds 는 이 에러를
// 받으면 live 세션 + 저장 인증 파일을 정리한다(재로그인 필요).
var ErrAuthLost = errors.New("login session lost during action; re-login required")
