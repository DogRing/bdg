import { useState } from 'react'
import Lobby from './views/Lobby'
import Game  from './views/Game'

// App 은 현재 어떤 화면을 보여줄지만 결정
// roomId 가 없으면 로비, 있으면 게임

export default function App() {
  const [roomId, setRoomId] = useState(null)

  if (!roomId) {
    return <Lobby onEnter={setRoomId} />
  }
  return <Game roomId={roomId} onLeave={() => setRoomId(null)} />
}