import { useState } from 'react'
import { AuthProvider, useAuth } from './context/AuthContext'
import Login from './views/Login'
import Lobby from './views/Lobby'
import Game  from './views/Game'

// ── 메인 라우터 ──────────────────────────────────────
// 인증 여부와 roomId 로 화면을 결정
//
// 향후 확장 계획:
//   Login → GameLobby (게임 종류 선택) → ChessLobby (방 목록) → Game
// 현재는 Login → Lobby → Game 구조
function AppRouter() {
  const { isLoggedIn } = useAuth()
  const [roomId, setRoomId] = useState(null)

  // 1) 로그인 안 된 상태 → 로그인 화면
  if (!isLoggedIn) {
    return <Login />
  }

  // 2) 로그인 됨 + 방 없음 → 로비
  if (!roomId) {
    return <Lobby onEnter={setRoomId} />
  }

  // 3) 로그인 됨 + 방 있음 → 게임 화면
  return <Game roomId={roomId} onLeave={() => setRoomId(null)} />
}

// ── App ──────────────────────────────────────────────
// AuthProvider 로 전체를 감싸서 하위 컴포넌트가 useAuth() 사용 가능
export default function App() {
  return (
    <AuthProvider>
      <AppRouter />
    </AuthProvider>
  )
}