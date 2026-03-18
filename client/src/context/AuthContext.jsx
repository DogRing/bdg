import { createContext, useContext, useState, useCallback, useEffect } from 'react'

const AUTH_API = import.meta.env.VITE_AUTH_URL || 'http://localhost:8080'

const AuthContext = createContext(null)

function AuthProvider({ children }) {
  const [token, setToken] = useState(() => localStorage.getItem('chess_token'))
  const [user, setUser]   = useState(null)

  useEffect(() => {
    if (token) {
      localStorage.setItem('chess_token', token)
    } else {
      localStorage.removeItem('chess_token')
    }
  }, [token])

  const requestOTP = useCallback(async (email) => {
    const res = await fetch(`${AUTH_API}/auth/otp`, {
      method:  'POST',
      headers: { 'Content-Type': 'application/json' },
      body:    JSON.stringify({ email }),
    })
    const data = await res.json()
    if (!res.ok) throw new Error(data.error || 'OTP 발송 실패')
    return data
  }, [])

  const login = useCallback(async (email, otp) => {
    const res = await fetch(`${AUTH_API}/auth/login`, {
      method:  'POST',
      headers: { 'Content-Type': 'application/json' },
      body:    JSON.stringify({ email, otp }),
    })
    const data = await res.json()
    if (!res.ok) throw new Error(data.error || '로그인 실패')
    setToken(data.token)
    setUser({ email })
    return data
  }, [])

  const logout = useCallback(() => {
    setToken(null)
    setUser(null)
  }, [])

  const authFetch = useCallback(async (url, options = {}) => {
    const headers = {
      'Content-Type': 'application/json',
      ...(options.headers || {}),
    }
    if (token) {
      headers['Authorization'] = `Bearer ${token}`
    }
    const res = await fetch(url, { ...options, headers })
    if (res.status === 401) {
      setToken(null)
      setUser(null)
    }
    return res
  }, [token])

  const value = {
    token,
    user,
    isLoggedIn: !!token,
    requestOTP,
    login,
    logout,
    authFetch,
  }

  return (
    <AuthContext.Provider value={value}>
      {children}
    </AuthContext.Provider>
  )
}

function useAuth() {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error('useAuth 는 AuthProvider 내부에서만 사용 가능합니다')
  return ctx
}

export { AuthProvider, useAuth }