import { useState } from 'react'

const API = import.meta.env.VITE_API_URL ?? 'http://localhost:8080'

export default function Lobby({ onEnter }) {
  const [loading, setLoading] = useState(false)
  const [error,   setError]   = useState('')

  async function createGame() {
    setLoading(true)
    setError('')
    try {
      const res  = await fetch(`${API}/chess/api/games`, { method: 'POST' })
      const text = await res.text()
      const data = text ? JSON.parse(text) : {}
      if (!res.ok) throw new Error(data.error || `HTTP ${res.status}`)
      onEnter(data.roomId)
    } catch (e) {
      setError(e.message)
      setLoading(false)
    }
  }

  return (
    <div style={styles.wrap}>
      <h1 style={styles.title}>♟ CHESS</h1>
      <p style={styles.sub}>두 플레이어가 번갈아 가며 진행합니다</p>
      <button style={styles.btn} onClick={createGame} disabled={loading}>
        {loading ? '생성 중...' : '게임 시작'}
      </button>
      {error && <p style={styles.err}>{error}</p>}
    </div>
  )
}

const styles = {
  wrap:  { display:'flex', flexDirection:'column', alignItems:'center',
           justifyContent:'center', height:'100vh', gap:24,
           background:'#1a1a2e', color:'#eee', fontFamily:'monospace' },
  title: { fontSize:52, letterSpacing:6, color:'#e2b96f' },
  sub:   { color:'#666', fontSize:13 },
  btn:   { padding:'16px 56px', fontSize:20, fontFamily:'monospace',
           background:'#e2b96f', color:'#1a1a1a', border:'none',
           borderRadius:8, cursor:'pointer' },
  err:   { color:'#f08080', fontSize:13 },
}