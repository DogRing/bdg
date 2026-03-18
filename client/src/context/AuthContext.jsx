import { useState, useRef, useEffect } from 'react'
import { useAuth } from '../context/AuthContext'

// ── 체스 기물 장식용 배열 ─────────────────────────────
const DECO_PIECES = ['♜','♞','♝','♛','♚','♝','♞','♜']

export default function Login() {
  const { requestOTP, login } = useAuth()

  // step: 'email' → 이메일 입력, 'otp' → 인증코드 입력
  const [step,    setStep]    = useState('email')
  const [email,   setEmail]   = useState('')
  const [otp,     setOtp]     = useState(['','','','','',''])
  const [loading, setLoading] = useState(false)
  const [error,   setError]   = useState('')
  const [countdown, setCountdown] = useState(0)

  const otpRefs = useRef([])

  // ── OTP 재발송 카운트다운 ────────────────────────
  useEffect(() => {
    if (countdown <= 0) return
    const t = setTimeout(() => setCountdown(c => c - 1), 1000)
    return () => clearTimeout(t)
  }, [countdown])

  // ── Step 1: 이메일 제출 → OTP 발송 ──────────────
  async function handleEmailSubmit(e) {
    e.preventDefault()
    if (!email.trim()) return
    setLoading(true)
    setError('')
    try {
      await requestOTP(email.trim())
      setStep('otp')
      setCountdown(300)  // 5분
    } catch (err) {
      setError(err.message)
    } finally {
      setLoading(false)
    }
  }

  // ── Step 2: OTP 입력 처리 ───────────────────────
  function handleOtpChange(index, value) {
    // 숫자만 허용
    if (value && !/^\d$/.test(value)) return

    const next = [...otp]
    next[index] = value
    setOtp(next)

    // 다음 칸으로 자동 포커스
    if (value && index < 5) {
      otpRefs.current[index + 1]?.focus()
    }

    // 6자리 모두 입력되면 자동 제출
    if (value && index === 5) {
      const code = next.join('')
      if (code.length === 6) submitOtp(code)
    }
  }

  function handleOtpKeyDown(index, e) {
    // Backspace 로 이전 칸 이동
    if (e.key === 'Backspace' && !otp[index] && index > 0) {
      otpRefs.current[index - 1]?.focus()
    }
  }

  // 붙여넣기 지원
  function handleOtpPaste(e) {
    e.preventDefault()
    const pasted = e.clipboardData.getData('text').replace(/\D/g, '').slice(0, 6)
    if (!pasted) return
    const next = [...otp]
    for (let i = 0; i < 6; i++) {
      next[i] = pasted[i] || ''
    }
    setOtp(next)
    if (pasted.length === 6) submitOtp(pasted)
  }

  async function submitOtp(code) {
    setLoading(true)
    setError('')
    try {
      await login(email.trim(), code)
      // 로그인 성공 → AuthContext 의 token 이 세팅되어 App 이 자동 전환
    } catch (err) {
      setError(err.message)
      setOtp(['','','','','',''])
      otpRefs.current[0]?.focus()
    } finally {
      setLoading(false)
    }
  }

  // ── OTP 재발송 ──────────────────────────────────
  async function resendOtp() {
    if (countdown > 0) return
    setLoading(true)
    setError('')
    try {
      await requestOTP(email.trim())
      setCountdown(300)
      setOtp(['','','','','',''])
      otpRefs.current[0]?.focus()
    } catch (err) {
      setError(err.message)
    } finally {
      setLoading(false)
    }
  }

  // ── 남은 시간 포매팅 ────────────────────────────
  function fmtTime(s) {
    const m = Math.floor(s / 60)
    const sec = s % 60
    return `${m}:${String(sec).padStart(2, '0')}`
  }

  return (
    <div style={styles.wrap}>

      {/* 배경 장식 — 흐릿한 체스 기물들 */}
      <div style={styles.decoRow}>
        {DECO_PIECES.map((p, i) => (
          <span key={i} style={{
            ...styles.decoPiece,
            animationDelay: `${i * 0.15}s`,
            opacity: 0.04 + (i % 3) * 0.01,
          }}>{p}</span>
        ))}
      </div>

      {/* 카드 */}
      <div style={styles.card}>

        {/* 헤더 */}
        <div style={styles.logoArea}>
          <span style={styles.logoIcon}>♚</span>
          <h1 style={styles.title}>CHESS</h1>
        </div>

        {step === 'email' ? (
          /* ── 이메일 입력 단계 ──────────────── */
          <>
            <p style={styles.desc}>이메일을 입력하면 로그인 코드를 보내드립니다</p>

            <form onSubmit={handleEmailSubmit} style={styles.form}>
              <label style={styles.label}>이메일</label>
              <input
                type="email"
                value={email}
                onChange={e => setEmail(e.target.value)}
                placeholder="you@example.com"
                autoFocus
                required
                style={styles.input}
                disabled={loading}
              />
              <button
                type="submit"
                style={{
                  ...styles.btn,
                  opacity: loading ? 0.6 : 1,
                  cursor: loading ? 'wait' : 'pointer',
                }}
                disabled={loading}
              >
                {loading ? '발송 중…' : '인증코드 받기'}
              </button>
            </form>
          </>
        ) : (
          /* ── OTP 입력 단계 ─────────────────── */
          <>
            <p style={styles.desc}>
              <strong style={{ color: '#e2b96f' }}>{email}</strong> 으로<br />
              6자리 인증코드를 보냈습니다
            </p>

            <div style={styles.otpWrap} onPaste={handleOtpPaste}>
              {otp.map((digit, i) => (
                <input
                  key={i}
                  ref={el => otpRefs.current[i] = el}
                  type="text"
                  inputMode="numeric"
                  maxLength={1}
                  value={digit}
                  onChange={e => handleOtpChange(i, e.target.value)}
                  onKeyDown={e => handleOtpKeyDown(i, e)}
                  autoFocus={i === 0}
                  disabled={loading}
                  style={{
                    ...styles.otpInput,
                    borderColor: digit ? '#e2b96f' : '#333',
                  }}
                />
              ))}
            </div>

            {/* 카운트다운 + 재발송 */}
            <div style={styles.timerRow}>
              {countdown > 0 ? (
                <span style={styles.timer}>남은 시간 {fmtTime(countdown)}</span>
              ) : (
                <span style={styles.expired}>인증코드가 만료되었습니다</span>
              )}
              <button
                onClick={resendOtp}
                disabled={loading || countdown > 240}
                style={{
                  ...styles.resendBtn,
                  opacity: (loading || countdown > 240) ? 0.3 : 1,
                }}
              >
                재발송
              </button>
            </div>

            {/* 이메일 변경 */}
            <button
              onClick={() => { setStep('email'); setError(''); setOtp(['','','','','','']) }}
              style={styles.backLink}
            >
              ← 다른 이메일로 변경
            </button>
          </>
        )}

        {/* 에러 메시지 */}
        {error && <p style={styles.error}>{error}</p>}
      </div>

      {/* 하단 텍스트 */}
      <p style={styles.footer}>비밀번호 없이 이메일만으로 로그인</p>
    </div>
  )
}

// ── 스타일 ────────────────────────────────────────────
const styles = {
  wrap: {
    display: 'flex',
    flexDirection: 'column',
    alignItems: 'center',
    justifyContent: 'center',
    minHeight: '100vh',
    background: '#1a1a2e',
    fontFamily: "'JetBrains Mono', 'Fira Code', 'SF Mono', monospace",
    color: '#ccc',
    position: 'relative',
    overflow: 'hidden',
  },

  /* 배경 장식 */
  decoRow: {
    position: 'absolute',
    top: '50%',
    left: '50%',
    transform: 'translate(-50%, -50%)',
    display: 'flex',
    gap: 48,
    pointerEvents: 'none',
    userSelect: 'none',
  },
  decoPiece: {
    fontSize: 120,
    color: '#e2b96f',
    filter: 'blur(1px)',
  },

  /* 카드 */
  card: {
    position: 'relative',
    zIndex: 1,
    width: 380,
    maxWidth: 'calc(100vw - 48px)',
    background: 'rgba(26, 26, 46, 0.92)',
    backdropFilter: 'blur(24px)',
    border: '1px solid rgba(226, 185, 111, 0.15)',
    borderRadius: 16,
    padding: '48px 36px 36px',
    display: 'flex',
    flexDirection: 'column',
    alignItems: 'center',
    boxShadow: '0 24px 80px rgba(0,0,0,0.5), inset 0 1px 0 rgba(226,185,111,0.08)',
  },

  /* 로고 */
  logoArea: {
    display: 'flex',
    alignItems: 'center',
    gap: 12,
    marginBottom: 28,
  },
  logoIcon: {
    fontSize: 36,
    color: '#e2b96f',
    filter: 'drop-shadow(0 0 8px rgba(226,185,111,0.4))',
  },
  title: {
    fontSize: 28,
    letterSpacing: 8,
    color: '#e2b96f',
    margin: 0,
    fontWeight: 600,
  },

  /* 텍스트 */
  desc: {
    fontSize: 13,
    color: '#888',
    textAlign: 'center',
    lineHeight: 1.6,
    marginBottom: 24,
  },

  /* 폼 */
  form: {
    width: '100%',
    display: 'flex',
    flexDirection: 'column',
    gap: 10,
  },
  label: {
    fontSize: 11,
    color: '#666',
    textTransform: 'uppercase',
    letterSpacing: 2,
  },
  input: {
    width: '100%',
    padding: '14px 16px',
    fontSize: 15,
    fontFamily: 'inherit',
    background: '#0d0d1a',
    border: '1px solid #333',
    borderRadius: 8,
    color: '#eee',
    outline: 'none',
    transition: 'border-color 0.2s',
    boxSizing: 'border-box',
  },
  btn: {
    marginTop: 8,
    padding: '14px 0',
    fontSize: 15,
    fontFamily: 'inherit',
    fontWeight: 600,
    background: '#e2b96f',
    color: '#1a1a2e',
    border: 'none',
    borderRadius: 8,
    letterSpacing: 1,
    transition: 'opacity 0.2s',
  },

  /* OTP 입력 */
  otpWrap: {
    display: 'flex',
    gap: 8,
    marginBottom: 20,
  },
  otpInput: {
    width: 44,
    height: 52,
    textAlign: 'center',
    fontSize: 22,
    fontFamily: 'inherit',
    fontWeight: 700,
    background: '#0d0d1a',
    border: '2px solid #333',
    borderRadius: 8,
    color: '#e2b96f',
    outline: 'none',
    caretColor: '#e2b96f',
    transition: 'border-color 0.2s',
  },

  /* 타이머 / 재발송 */
  timerRow: {
    display: 'flex',
    alignItems: 'center',
    gap: 12,
    marginBottom: 16,
  },
  timer: {
    fontSize: 12,
    color: '#666',
  },
  expired: {
    fontSize: 12,
    color: '#f08080',
  },
  resendBtn: {
    fontSize: 12,
    fontFamily: 'inherit',
    color: '#e2b96f',
    background: 'none',
    border: '1px solid rgba(226,185,111,0.3)',
    borderRadius: 6,
    padding: '4px 12px',
    cursor: 'pointer',
    transition: 'opacity 0.2s',
  },

  /* 뒤로가기 */
  backLink: {
    fontSize: 12,
    fontFamily: 'inherit',
    color: '#555',
    background: 'none',
    border: 'none',
    cursor: 'pointer',
    marginTop: 4,
  },

  /* 에러 */
  error: {
    marginTop: 16,
    fontSize: 13,
    color: '#f08080',
    textAlign: 'center',
  },

  /* 하단 */
  footer: {
    position: 'relative',
    zIndex: 1,
    marginTop: 24,
    fontSize: 11,
    color: '#444',
    letterSpacing: 1,
  },
}