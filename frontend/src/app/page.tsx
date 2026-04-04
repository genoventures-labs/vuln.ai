'use client';

import { useState } from 'react';

interface Finding {
  id: string;
  file: string;
  line: number;
  vuln_type: string;
  severity: string;
  description: string;
  snippet: string;
  ai_analysis?: string;
}

export default function Home() {
  const [path, setPath] = useState('');
  const [findings, setFindings] = useState<Finding[]>([]);
  const [loading, setLoading] = useState(false);
  const [aiEnabled, setAiEnabled] = useState(false);
  const [error, setError] = useState('');

  const handleScan = async () => {
    setLoading(true);
    setError('');
    try {
      const response = await fetch('http://localhost:8080/api/scan', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({ path, enable_ai: aiEnabled }),
      });

      const data = await response.json();
      if (data.success) {
        setFindings(data.findings || []);
      } else {
        setError(data.error || 'Failed to complete scan');
      }
    } catch (err) {
      setError('Failed to connect to backend. Make sure it is running on port 8080.');
    } finally {
      setLoading(false);
    }
  };

  return (
    <main className="dashboard">
      <header className="header">
        <div className="title-group">
          <h1>VULN.AI</h1>
          <p>Vulnerability Assessment Suite</p>
        </div>
        <div className="ai-toggle">
          <label className="toggle-switch">
            <input
              type="checkbox"
              checked={aiEnabled}
              onChange={(e) => setAiEnabled(e.target.checked)}
            />
            <span className="slider"></span>
          </label>
          <span style={{ color: aiEnabled ? 'var(--primary-color)' : 'var(--text-muted)', fontWeight: 600 }}>
            AI ENHANCEMENTS {aiEnabled ? 'ON' : 'OFF'}
          </span>
        </div>
      </header>

      <section className="card">
        <h2>Target Scan</h2>
        <p style={{ color: 'var(--text-muted)', marginBottom: '1rem' }}>Enter the absolute path to the directory or file you want to scan.</p>
        <div className="scan-section">
          <input
            type="text"
            placeholder="/path/to/your/source/code"
            value={path}
            onChange={(e) => setPath(e.target.value)}
          />
          <button onClick={handleScan} disabled={loading || !path}>
            {loading ? 'SCANNING...' : 'START SCAN'}
          </button>
        </div>
        {error && <p style={{ color: 'var(--error-color)', marginTop: '1rem' }}>{error}</p>}
      </section>

      {findings.length > 0 && (
        <section className="card">
          <h2>Findings ({findings.length})</h2>
          <table className="results-table">
            <thead>
              <tr>
                <th>Vulnerability</th>
                <th>Severity</th>
                <th>Location</th>
                <th>Snippet</th>
                <th>AI Analysis</th>
              </tr>
            </thead>
            <tbody>
              {findings.map((f, i) => (
                <tr key={`${f.id}-${i}`}>
                  <td>
                    <div style={{ fontWeight: 600 }}>{f.vuln_type}</div>
                    <div style={{ fontSize: '0.8rem', color: 'var(--text-muted)' }}>{f.id}</div>
                  </td>
                  <td>
                    <span className={`severity ${f.severity.toLowerCase()}`}>
                      {f.severity}
                    </span>
                  </td>
                  <td>
                    <div style={{ fontSize: '0.9rem' }}>{f.file.split('/').pop()}</div>
                    <div style={{ fontSize: '0.8rem', color: 'var(--text-muted)' }}>Line {f.line}</div>
                  </td>
                  <td>
                    <div className="snippet">{f.snippet}</div>
                  </td>
                  <td>
                    {f.ai_analysis ? (
                      <div className="ai-analysis-text">{f.ai_analysis}</div>
                    ) : (
                      <span style={{ color: 'var(--text-muted)', fontSize: '0.8rem' }}>
                        {aiEnabled ? 'Analyzing...' : 'AI Disabled'}
                      </span>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </section>
      )}

      {findings.length === 0 && !loading && !error && path && (
        <div className="card" style={{ textAlign: 'center', padding: '3rem' }}>
          <p style={{ color: 'var(--text-muted)' }}>No vulnerabilities found yet. Enter a path and start scanning.</p>
        </div>
      )}
    </main>
  );
}
