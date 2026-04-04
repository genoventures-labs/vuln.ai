'use client';

import { useState, useEffect, useCallback } from 'react';
import {
  PlusIcon,
  RefreshIcon,
  TargetIcon,
  XIcon,
  CheckCircleIcon,
  CircleIcon,
  LoaderIcon,
  AlertTriangleIcon,
} from '@/components/icons';

interface HistoryEntry {
  timestamp: string;
  phase: string;
  message: string;
}

interface Mission {
  id: string;
  target_url: string;
  description: string;
  status: string;
  created_at: string;
  history?: HistoryEntry[];
  payload?: string;
  verdict?: string;
  patch?: string;
}

const PHASES = ['Recon', 'Strike', 'Patch', 'Verify'] as const;

function getPhaseStatus(
  mission: Mission,
  phase: string
): 'done' | 'active' | 'failed' | 'pending' {
  if (mission.status === 'Failed') return 'failed';
  if (mission.status === 'Resolved') return 'done';

  const order: Record<string, number> = { Recon: 0, Strike: 1, Patch: 2, Verify: 3 };
  const phaseIdx = order[phase] ?? 0;

  if (mission.status === 'Exploited') return phaseIdx <= 1 ? 'done' : 'pending';
  if (mission.status === 'Patched') return phaseIdx <= 2 ? 'done' : 'pending';
  if (mission.status === 'Pending') return 'pending';

  const currentIdx = order[mission.status] ?? -1;
  if (phaseIdx < currentIdx) return 'done';
  if (phaseIdx === currentIdx) return 'active';
  return 'pending';
}

const ACTIVE_STATUSES = new Set(['Pending', 'Recon', 'Strike', 'Patch', 'Verify']);

export default function MissionsPage() {
  const [missions, setMissions] = useState<Mission[]>([]);
  const [selected, setSelected] = useState<Mission | null>(null);
  const [showForm, setShowForm] = useState(false);
  const [targetUrl, setTargetUrl] = useState('');
  const [description, setDescription] = useState('');
  const [launching, setLaunching] = useState(false);
  const [formError, setFormError] = useState('');

  const fetchMissions = useCallback(async () => {
    try {
      const res = await fetch('http://localhost:8080/api/missions');
      const data = await res.json();
      setMissions(Array.isArray(data) ? data : []);
    } catch {
      // backend offline — leave list unchanged
    }
  }, []);

  const refreshSelected = useCallback(async (id: string) => {
    try {
      const res = await fetch(`http://localhost:8080/api/missions/${id}`);
      const data = await res.json();
      setSelected(data);
      setMissions((prev) =>
        prev.map((m) => (m.id === id ? data : m))
      );
    } catch {
      // ignore
    }
  }, []);

  useEffect(() => {
    fetchMissions();
  }, [fetchMissions]);

  // Poll when selected mission is active
  useEffect(() => {
    if (!selected || !ACTIVE_STATUSES.has(selected.status)) return;
    const interval = setInterval(() => refreshSelected(selected.id), 5000);
    return () => clearInterval(interval);
  }, [selected, refreshSelected]);

  const handleLaunch = async () => {
    setLaunching(true);
    setFormError('');
    try {
      const res = await fetch('http://localhost:8080/api/missions', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ target_url: targetUrl, description }),
      });
      const data = await res.json();
      setMissions((prev) => [data, ...prev]);
      setSelected(data);
      setShowForm(false);
      setTargetUrl('');
      setDescription('');
    } catch {
      setFormError('Failed to launch. Backend unreachable on port 8080.');
    } finally {
      setLaunching(false);
    }
  };

  return (
    <div className="page-content">
      <div className="page-header">
        <div>
          <h1 className="page-title">Mission Control</h1>
          <p className="page-subtitle">Automated pentest orchestration — Recon → Strike → Patch → Verify</p>
        </div>
        <div className="header-actions">
          <button className="btn btn-ghost" onClick={fetchMissions} title="Refresh">
            <RefreshIcon size={16} />
          </button>
          <button
            className="btn btn-primary"
            onClick={() => { setShowForm(!showForm); setFormError(''); }}
          >
            <PlusIcon size={16} />
            Launch Mission
          </button>
        </div>
      </div>

      {showForm && (
        <div className="card form-card">
          <h2 className="card-title">New Mission</h2>
          <div className="form-group">
            <label>Target URL</label>
            <input
              type="text"
              placeholder="http://target.host:8080"
              value={targetUrl}
              onChange={(e) => setTargetUrl(e.target.value)}
              onKeyDown={(e) => e.key === 'Enter' && targetUrl && !launching && handleLaunch()}
            />
          </div>
          <div className="form-group">
            <label>Description</label>
            <input
              type="text"
              placeholder="Brief description of the target"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
            />
          </div>
          {formError && <p className="error-text">{formError}</p>}
          <div className="form-actions">
            <button className="btn btn-outline" onClick={() => setShowForm(false)}>
              Cancel
            </button>
            <button
              className="btn btn-primary"
              onClick={handleLaunch}
              disabled={launching || !targetUrl}
            >
              {launching ? 'Launching...' : 'Launch Mission'}
            </button>
          </div>
        </div>
      )}

      <div className={`missions-layout${selected ? ' missions-layout-split' : ''}`}>
        <div className="missions-list">
          <div className="card" style={{ padding: 0, overflow: 'hidden' }}>
            {missions.length === 0 ? (
              <div className="empty-state">
                <TargetIcon size={32} color="var(--text-muted)" />
                <p>No missions yet. Launch one to get started.</p>
              </div>
            ) : (
              <table className="results-table">
                <thead>
                  <tr>
                    <th>Target</th>
                    <th>Status</th>
                    <th>Created</th>
                  </tr>
                </thead>
                <tbody>
                  {missions.map((m) => (
                    <tr
                      key={m.id}
                      className={`clickable-row${selected?.id === m.id ? ' row-selected' : ''}`}
                      onClick={() => setSelected(m)}
                    >
                      <td>
                        <div style={{ fontWeight: 600, fontSize: '0.9rem' }}>{m.target_url}</div>
                        <div style={{ fontSize: '0.75rem', color: 'var(--text-muted)' }}>{m.id}</div>
                      </td>
                      <td>
                        <span className={`mission-status mission-status-${m.status.toLowerCase()}`}>
                          {m.status}
                        </span>
                      </td>
                      <td style={{ fontSize: '0.8rem', color: 'var(--text-muted)', whiteSpace: 'nowrap' }}>
                        {m.created_at ? new Date(m.created_at).toLocaleString() : '—'}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
        </div>

        {selected && (
          <div className="card mission-detail">
            <div className="detail-header">
              <div style={{ minWidth: 0 }}>
                <div style={{ fontWeight: 700, fontSize: '0.95rem', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                  {selected.target_url}
                </div>
                <div style={{ fontSize: '0.75rem', color: 'var(--text-muted)', marginTop: '0.125rem' }}>
                  {selected.id}
                </div>
              </div>
              <button className="btn-icon" onClick={() => setSelected(null)} aria-label="Close">
                <XIcon size={16} />
              </button>
            </div>

            <div className="phase-timeline">
              {PHASES.map((phase, idx) => {
                const status = getPhaseStatus(selected, phase);
                const prevStatus = idx > 0 ? getPhaseStatus(selected, PHASES[idx - 1]) : null;
                return (
                  <div key={phase} style={{ display: 'contents' }}>
                    {idx > 0 && (
                      <div
                        className={`phase-connector${prevStatus === 'done' ? ' phase-connector-done' : prevStatus === 'failed' ? ' phase-connector-failed' : ''}`}
                      />
                    )}
                    <div className={`phase-step phase-${status}`}>
                      <div className="phase-icon">
                        {status === 'done' && <CheckCircleIcon size={16} />}
                        {status === 'active' && <LoaderIcon size={16} />}
                        {status === 'failed' && <XIcon size={16} />}
                        {status === 'pending' && <CircleIcon size={16} />}
                      </div>
                      <span className="phase-label">{phase}</span>
                    </div>
                  </div>
                );
              })}
            </div>

            <div style={{ display: 'flex', alignItems: 'center', gap: '0.5rem', marginBottom: '1.25rem' }}>
              <span className={`mission-status mission-status-${selected.status.toLowerCase()}`}>
                {selected.status}
              </span>
              {ACTIVE_STATUSES.has(selected.status) && (
                <span style={{ fontSize: '0.75rem', color: 'var(--text-muted)' }}>
                  Polling every 5s
                </span>
              )}
            </div>

            {selected.payload && (
              <div className="detail-section">
                <h4>Payload</h4>
                <div className="snippet">{selected.payload}</div>
              </div>
            )}

            {selected.verdict && (
              <div className="detail-section">
                <h4>Verdict</h4>
                <p style={{ fontSize: '0.875rem', color: 'var(--text-color)', lineHeight: 1.6 }}>
                  {selected.verdict}
                </p>
              </div>
            )}

            {selected.patch && (
              <div className="detail-section">
                <h4>Patch Applied</h4>
                <div className="snippet">{selected.patch}</div>
              </div>
            )}

            {selected.history && selected.history.length > 0 && (
              <div className="detail-section">
                <h4>History Log</h4>
                <div className="history-log">
                  {selected.history.map((entry, i) => (
                    <div key={i} className="history-entry">
                      <span className="history-time">
                        {new Date(entry.timestamp).toLocaleTimeString()}
                      </span>
                      <span className="history-phase">{entry.phase}</span>
                      <span style={{ fontSize: '0.8rem', color: 'var(--text-muted)' }}>
                        {entry.message}
                      </span>
                    </div>
                  ))}
                </div>
              </div>
            )}

            {!selected.payload && !selected.verdict && !selected.patch &&
              (!selected.history || selected.history.length === 0) && (
                <div style={{ textAlign: 'center', padding: '1.5rem 0' }}>
                  <AlertTriangleIcon size={24} color="var(--text-muted)" />
                  <p style={{ fontSize: '0.875rem', color: 'var(--text-muted)', marginTop: '0.5rem' }}>
                    No details available yet.
                  </p>
                </div>
              )}
          </div>
        )}
      </div>
    </div>
  );
}
