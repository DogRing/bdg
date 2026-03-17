import { useState, useEffect, useRef } from 'react'
import Board from '../components/Board'

const API = import.meta.env.VITE_API_URL ?? 'http://localhost:8080'
const SSE = import.meta.env.VITE_SSE_URL ?? 'http://localhost:3001'

export default function Game({ roomId, onLeave }) {
  const [board,    setBoard]    = useState([])      // 2D 배열
  const [turn,     setTurn]     = useState('w')     // 'w' | 'b'
  const [selected, setSelected] = useState(null)    // { ri, fi }
  const [connected,setConnected]= useState(false)
  const [logs,     setLogs]     = useState([])
  const esRef = useRef(null)

  // ── SSE 연결 ────────────────────────────────
  useEffect(() => {
    const es = new EventSource(`${SSE}/sse/room/${roomId}`)
    esRef.current = es

    es.onopen  = () => setConnected(true)
    es.onerror = () => setConnected(false)

    es.addEventListener('snapshot', e => {
      const data = JSON.parse(e.data)
      setBoard(data.board)
      addLog('📸 snapshot 수신')
    })

    es.addEventListener('board_update', e => {
      const data = JSON.parse(e.data)
      setBoard(data.board)
      addLog('♟ 보드 업데이트')
    })

    // 컴포넌트가 사라질 때 SSE 연결 해제
    return () => es.close()
  }, [roomId])

  // ── 말 이동 ─────────────────────────────────
  async function move(from, to) {
    try {
      const res  = await fetch(`${API}/api/games/${roomId}/move`, {
        method:  'POST',
        headers: { 'Content-Type': 'application/json' },
        body:    JSON.stringify({ from, to }),
      })
      const text = await res.text()
      const data = text ? JSON.parse(text) : {}
      if (data.error) { addLog(`❌ ${data.error}`); return }
      setTurn(data.turn)
      addLog(`✅ ${from} → ${to}`)
    } catch {
      addLog('❌ 이동 요청 실패')
    }
  }

  // ── 셀 클릭 처리 ────────────────────────────
  function onCellClick(ri, fi) {
    const piece = board[ri]?.[fi]
    if (!piece) return

    // 아무것도 선택 안 된 상태 → 말 선택
    if (!selected) {
      if (piece === '.') return
      setSelected({ ri, fi })
      return
    }

    // 같은 칸 클릭 → 선택 해제
    if (selected.ri === ri && selected.fi === fi) {
      setSelected(null)
      return
    }

    // 다른 칸 클릭 → 이동 요청
    const FILES = ['a','b','c','d','e','f','g','h']
    const from  = FILES[selected.fi] + (8 - selected.ri)
    const to    = FILES[fi]          + (8 - ri)
    setSelected(null)
    move(from, to)
  }

  function addLog(msg) {
    const time = new Date().toLocaleTimeString()
    setLogs(prev => [`${time}  ${msg}`, ...prev].slice(0, 30))
  }

  return (
    <div style={styles.wrap}>

      {/* 헤더 */}
      <div style={styles.header}>
        <span style={{ ...styles.dot, background: connected ? '#52b788' : '#f08080' }} />
        <span style={{ ...styles.turnBadge, ...(turn === 'w' ? styles.turnW : styles.turnB) }}>
          {turn === 'w' ? '⬜ 백 차례' : '⬛ 흑 차례'}
        </span>
        <span style={styles.roomLabel}>room: {roomId.slice(0, 8)}…</span>
        <button style={styles.backBtn} onClick={onLeave}>← 나가기</button>
      </div>

      {/* 체스판 */}
      <Board board={board} selected={selected} onCellClick={onCellClick} />

      {/* 로그 */}
      <div style={styles.log}>
        {logs.map((l, i) => <div key={i}>{l}</div>)}
      </div>

    </div>
  )
}

const styles = {
  wrap:      { display:'flex', flexDirection:'column', alignItems:'center',
               padding:24, gap:12, minHeight:'100vh',
               background:'#1a1a2e', color:'#eee', fontFamily:'monospace' },
  header:    { display:'flex', alignItems:'center', gap:16, fontSize:13 },
  dot:       { width:8, height:8, borderRadius:'50%', display:'inline-block' },
  turnBadge: { padding:'4px 14px', borderRadius:20, fontWeight:'bold', fontSize:13 },
  turnW:     { background:'#f0d9b5', color:'#1a1a1a' },
  turnB:     { background:'#2a2a2a', color:'#f0d9b5', border:'1px solid #555' },
  roomLabel: { color:'#444', fontSize:11 },
  backBtn:   { background:'none', border:'none', color:'#555',
               cursor:'pointer', fontFamily:'monospace', fontSize:12 },
  log:       { width:580, background:'#0d1117', border:'1px solid #21262d',
               borderRadius:6, padding:'8px 12px', height:90,
               overflowY:'auto', fontSize:11, color:'#8b949e' },
}