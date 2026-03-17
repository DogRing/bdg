const FILES = ['a','b','c','d','e','f','g','h']

const SYM = {
  '.':'',
  WP:'♙', WN:'♘', WB:'♗', WR:'♖', WQ:'♕', WK:'♔',
  BP:'♟', BN:'♞', BB:'♝', BR:'♜', BQ:'♛', BK:'♚',
}

export default function Board({ board, selected, onCellClick }) {
  if (!board.length) return null

  return (
    <table style={styles.table}>
      <tbody>
        {board.map((row, ri) => {
          const rankNum = 8 - ri
          return (
            <tr key={ri}>
              {/* 좌측 랭크 라벨 */}
              <td style={styles.label}>{rankNum}</td>

              {row.map((piece, fi) => {
                const isDark = (ri + fi) % 2 === 1
                const isSel  = selected?.ri === ri && selected?.fi === fi
                const isWhite = piece.startsWith('W')

                return (
                  <td
                    key={fi}
                    onClick={() => onCellClick(ri, fi)}
                    style={{
                      ...styles.cell,
                      background: isDark ? '#b58863' : '#f0d9b5',
                      outline: isSel ? '3px solid #f6f669' : 'none',
                      outlineOffset: '-3px',
                    }}
                  >
                    {/* 기물 */}
                    {piece !== '.' && (
                      <span style={{
                        ...styles.piece,
                        filter: isWhite
                          ? 'drop-shadow(0 1px 2px rgba(0,0,0,0.8))'
                          : 'drop-shadow(0 1px 1px rgba(255,255,255,0.3))',
                      }}>
                        {SYM[piece]}
                      </span>
                    )}
                    {/* 좌표 */}
                    <span style={{
                      ...styles.coord,
                      color: isDark ? 'rgba(255,255,255,0.3)' : 'rgba(0,0,0,0.3)',
                    }}>
                      {FILES[fi]}{rankNum}
                    </span>
                  </td>
                )
              })}

              {/* 우측 랭크 라벨 */}
              <td style={styles.label}>{rankNum}</td>
            </tr>
          )
        })}

        {/* 파일 라벨 행 */}
        <tr>
          <td />
          {FILES.map(f => (
            <td key={f} style={styles.fileLabel}>{f}</td>
          ))}
          <td />
        </tr>
      </tbody>
    </table>
  )
}

const styles = {
  table: {
    borderCollapse: 'collapse',
    border: '2px solid #444',
  },
  cell: {
    width: 72, height: 72,
    textAlign: 'center', verticalAlign: 'middle',
    cursor: 'pointer',
    position: 'relative',
  },
  piece: {
    fontSize: 40,
    lineHeight: 1,
    userSelect: 'none',
    display: 'block',
  },
  coord: {
    position: 'absolute',
    bottom: 2, right: 3,
    fontSize: 9,
    pointerEvents: 'none',
  },
  label: {
    width: 20,
    textAlign: 'center',
    color: '#555',
    fontSize: 11,
    fontFamily: 'monospace',
  },
  fileLabel: {
    height: 20,
    textAlign: 'center',
    color: '#555',
    fontSize: 11,
    fontFamily: 'monospace',
  },
}