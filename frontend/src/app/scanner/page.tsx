'use client';

import { Fragment, useState } from 'react';
import {
  ScanIcon,
  DownloadIcon,
  ChevronRightIcon,
  ChevronDownIcon,
  AlertTriangleIcon,
} from '@/components/icons';

interface Finding {
  id: string;
  file: string;
  line: number;
  vuln_type: string;
  severity: string;
  description: string;
  snippet: string;
  ai_analysis?: string;
  taint_trace?: string;
}

const SCAN_TYPES = ['Static Analysis', 'Supply Chain', 'Auth Flow', 'Full Scan'] as const;
type ScanType = (typeof SCAN_TYPES)[number];

const SEVERITIES = ['All', 'Critical', 'High', 'Medium', 'Low'] as const;
type SeverityFilter = (typeof SEVERITIES)[number];

export default function ScannerPage() {
  const [path, setPath] = useState('');
  const [scanType, setScanType] = useState<ScanType>('Static Analysis');
  const [severityFilter, setSeverityFilter] = useState<SeverityFilter>('All');
  const [findings, setFindings] = useState<Finding[]>([]);
  const [loading, setLoading] = useState(false);
  const [aiEnabled, setAiEnabled] = useState(false);
  const [error, setError] = useState('');
  const [expandedRow, setExpandedRow] = useState<string | null>(null);

  const handleScan = async () => {
    setLoading(true);
    setError('');
    setFindings([]);
    setExpandedRow(null);
    try {
      const response = await fetch('http://localhost:8080/api/scan', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ path, enable_ai: aiEnabled }),
      });
      const data = await response.json();
      if (data.success) {
        setFindings(data.findings || []);
      } else {
        setError(data.error || 'Scan failed');
      }
    } catch {
      setError('Failed to connect to backend on port 8080.');
    } finally {
      setLoading(false);
    }
  };

  const filteredFindings = findings.filter((f) => {
    if (severityFilter === 'All') return true;
    return f.severity.toLowerCase() === severityFilter.toLowerCase();
  });

  const toggleRow = (key: string) => setExpandedRow(expandedRow === key ? null : key);

  const getSeverityCount = (sev: string) =>
    sev === 'All'
      ? findings.length
      : findings.filter((f) => f.severity.toLowerCase() === sev.toLowerCase()).length;

  return (
    <div className="page-content">
      <div className="page-header">
        <div>
          <h1 className="page-title">Code Scanner</h1>
          <p className="page-subtitle">Static analysis and vulnerability detection</p>
        </div>
        {findings.length > 0 && (
          <button className="btn btn-outline">
            <DownloadIcon size={15} />
            Export Findings
          </button>
        )}
      </div>

      <section className="card">
        <h2 className="card-title">Scan Configuration</h2>

        <div className="form-group">
          <label>Scan Type</label>
          <div className="scan-type-selector">
            {SCAN_TYPES.map((t) => (
              <button
                key={t}
                className={`scan-type-btn${scanType === t ? ' active' : ''}`}
                onClick={() => setScanType(t)}
              >
                {t}
              </button>
            ))}
          </div>
        </div>

        <div className="form-group">
          <label>Target Path</label>
          <div className="scan-section">
            <input
              type="text"
              placeholder="/absolute/path/to/your/code"
              value={path}
              onChange={(e) => setPath(e.target.value)}
              onKeyDown={(e) => e.key === 'Enter' && path && !loading && handleScan()}
            />
            <button
              className="btn btn-primary"
              onClick={handleScan}
              disabled={loading || !path}
            >
              <ScanIcon size={16} />
              {loading ? 'Scanning...' : 'Start Scan'}
            </button>
          </div>
        </div>

        <div className="ai-toggle">
          <label className="toggle-switch">
            <input
              type="checkbox"
              checked={aiEnabled}
              onChange={(e) => setAiEnabled(e.target.checked)}
            />
            <span className="slider" />
          </label>
          <span
            style={{
              color: aiEnabled ? 'var(--primary-color)' : 'var(--text-muted)',
              fontWeight: 600,
              fontSize: '0.875rem',
            }}
          >
            AI ENHANCEMENTS {aiEnabled ? 'ON' : 'OFF'}
          </span>
        </div>

        {loading && (
          <div className="scan-progress">
            <div className="scan-progress-bar" />
          </div>
        )}

        {error && <p className="error-text" style={{ marginTop: '0.75rem' }}>{error}</p>}
      </section>

      {findings.length > 0 && (
        <section className="card">
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '1rem' }}>
            <h2 className="card-title" style={{ marginBottom: 0 }}>
              Findings ({filteredFindings.length}
              {severityFilter !== 'All' ? ` of ${findings.length}` : ''})
            </h2>
          </div>

          <div className="severity-filters">
            {SEVERITIES.map((s) => {
              const count = getSeverityCount(s);
              return (
                <button
                  key={s}
                  className={`severity-filter-btn ${s.toLowerCase()}${severityFilter === s ? ' active' : ''}`}
                  onClick={() => setSeverityFilter(s)}
                >
                  {s}
                  {count > 0 && <span style={{ opacity: 0.7, marginLeft: '0.25rem' }}>({count})</span>}
                </button>
              );
            })}
          </div>

          <table className="results-table">
            <thead>
              <tr>
                <th style={{ width: '28px' }} />
                <th>Vulnerability</th>
                <th>Severity</th>
                <th>Location</th>
                <th>Snippet</th>
              </tr>
            </thead>
            <tbody>
              {filteredFindings.map((f, i) => {
                const rowKey = `${f.id}-${i}`;
                const isExpanded = expandedRow === rowKey;
                return (
                  <Fragment key={rowKey}>
                    <tr
                      className="expandable-row"
                      onClick={() => toggleRow(rowKey)}
                    >
                      <td>
                        {isExpanded ? (
                          <ChevronDownIcon size={14} color="var(--text-muted)" />
                        ) : (
                          <ChevronRightIcon size={14} color="var(--text-muted)" />
                        )}
                      </td>
                      <td>
                        <div style={{ fontWeight: 600, fontSize: '0.9rem' }}>{f.vuln_type}</div>
                        <div style={{ fontSize: '0.75rem', color: 'var(--text-muted)' }}>{f.id}</div>
                      </td>
                      <td>
                        <span className={`severity ${f.severity.toLowerCase()}`}>{f.severity}</span>
                      </td>
                      <td>
                        <div style={{ fontSize: '0.875rem', fontFamily: 'monospace' }}>
                          {f.file.split('/').pop()}
                        </div>
                        <div style={{ fontSize: '0.75rem', color: 'var(--text-muted)' }}>
                          Line {f.line}
                        </div>
                      </td>
                      <td>
                        <div className="snippet" style={{ maxWidth: '260px' }}>{f.snippet}</div>
                      </td>
                    </tr>
                    {isExpanded && (
                      <tr>
                        <td colSpan={5} style={{ padding: '0 1rem 1rem' }}>
                          <div className="expanded-content">
                            {f.description && (
                              <div style={{ marginBottom: '0.875rem' }}>
                                <h4>Description</h4>
                                <p style={{ fontSize: '0.875rem', color: 'var(--text-muted)', lineHeight: 1.6 }}>
                                  {f.description}
                                </p>
                              </div>
                            )}
                            {f.ai_analysis && (
                              <div style={{ marginBottom: '0.875rem' }}>
                                <h4>AI Analysis</h4>
                                <p style={{
                                  fontSize: '0.875rem',
                                  color: 'var(--text-color)',
                                  lineHeight: 1.6,
                                  background: 'rgba(99,102,241,0.06)',
                                  padding: '0.75rem',
                                  borderRadius: '0.375rem',
                                  borderLeft: '2px solid var(--primary-color)',
                                }}>
                                  {f.ai_analysis}
                                </p>
                              </div>
                            )}
                            {f.taint_trace && (
                              <div>
                                <h4>Taint Trace</h4>
                                <div className="taint-trace">{f.taint_trace}</div>
                              </div>
                            )}
                          </div>
                        </td>
                      </tr>
                    )}
                  </Fragment>
                );
              })}
            </tbody>
          </table>
        </section>
      )}

      {findings.length === 0 && !loading && (
        <div className="card empty-state">
          <AlertTriangleIcon size={32} color="var(--text-muted)" />
          <p>{path ? 'No vulnerabilities found.' : 'Enter a path above to start scanning.'}</p>
        </div>
      )}
    </div>
  );
}
