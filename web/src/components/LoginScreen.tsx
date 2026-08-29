import { useState } from 'react';
import { ApiError, api } from '../api';
import type { User } from '../types';

type Mode = 'login' | 'register' | 'reset';

const MODES: { value: Mode; label: string }[] = [
  { value: 'login', label: 'Sign in' },
  { value: 'register', label: 'Register' },
  { value: 'reset', label: 'Reset' },
];

// Translate API failures into short, actionable messages for the form.
function describeError(err: unknown, mode: Mode): string {
  if (err instanceof ApiError) {
    switch (err.status) {
      case 401:
        return mode === 'login'
          ? 'Invalid email or password.'
          : err.message;
      case 422:
        return 'Your email is not verified. Check your inbox for the verification link.';
      case 409:
        return 'That email address is already registered.';
      case 429:
        return 'Too many attempts. Wait a minute and try again.';
      default:
        return err.message;
    }
  }
  return err instanceof Error ? err.message : 'Something went wrong.';
}

export default function LoginScreen({
  onLogin,
}: {
  onLogin: (user: User) => void;
}) {
  const [mode, setMode] = useState<Mode>('login');
  const [email, setEmail] = useState('');
  const [name, setName] = useState('');
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  const switchMode = (m: Mode) => {
    setMode(m);
    setError(null);
    setNotice(null);
  };

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    setNotice(null);
    try {
      if (mode === 'login') {
        onLogin(await api.login(email.trim(), password));
      } else if (mode === 'register') {
        const res = await api.register(email.trim(), name.trim(), password);
        setNotice(res.message || 'Verification email sent. Check your inbox.');
      } else {
        await api.forgotPassword(email.trim());
        setNotice('If that email is registered, a reset link is on its way.');
      }
    } catch (err) {
      setError(describeError(err, mode));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="auth">
      <div className="auth-card">
        <div className="auth-brand">elyfeed</div>
        <div className="auth-tabs" role="tablist" aria-label="Authentication">
          {MODES.map(({ value, label }) => (
            <button
              key={value}
              type="button"
              role="tab"
              aria-selected={mode === value}
              className={mode === value ? 'active' : ''}
              onClick={() => switchMode(value)}
            >
              {label}
            </button>
          ))}
        </div>

        <form className="auth-form" onSubmit={submit}>
          {mode === 'register' && (
            <label className="auth-field">
              <span>Name</span>
              <input
                type="text"
                value={name}
                onChange={(e) => setName(e.target.value)}
                autoComplete="name"
                required
              />
            </label>
          )}
          <label className="auth-field">
            <span>Email</span>
            <input
              type="email"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              autoComplete="email"
              required
            />
          </label>
          {mode !== 'reset' && (
            <label className="auth-field">
              <span>Password</span>
              <input
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                autoComplete={
                  mode === 'login' ? 'current-password' : 'new-password'
                }
                required
                minLength={mode === 'register' ? 8 : undefined}
              />
            </label>
          )}

          {notice && <div className="auth-notice">{notice}</div>}
          {error && <div className="auth-error">{error}</div>}

          <button className="auth-submit" type="submit" disabled={busy}>
            {busy
              ? 'Please wait…'
              : mode === 'login'
                ? 'Sign in'
                : mode === 'register'
                  ? 'Create account'
                  : 'Send reset link'}
          </button>
        </form>

        <div className="auth-alt">
          {mode === 'login' ? (
            <>
              <button
                type="button"
                className="link"
                onClick={() => switchMode('reset')}
              >
                Forgot password?
              </button>
              <button
                type="button"
                className="link"
                onClick={() => switchMode('register')}
              >
                Create an account
              </button>
            </>
          ) : (
            <button
              type="button"
              className="link"
              onClick={() => switchMode('login')}
            >
              Back to sign in
            </button>
          )}
        </div>
      </div>
    </div>
  );
}
