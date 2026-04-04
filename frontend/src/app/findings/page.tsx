'use client';

import { useMemo, useState } from 'react';
import {
  SearchIcon,
  DownloadIcon,
  XIcon,
  ChevronLeftIcon,
  ChevronRightIcon,
  ShieldIcon,
  FileIcon,
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
  patch_suggestion?: string;
}

const MOCK_FINDINGS: Finding[] = [
  {
    id: 'F-001', file: '/home/user/app/auth/login.py', line: 42,
    vuln_type: 'SQL Injection', severity: 'critical',
    description: 'User input directly interpolated into SQL query without sanitization.',
    snippet: 'query = "SELECT * FROM users WHERE email = \'"+email+"\'"',
    ai_analysis: 'This SQL injection vulnerability allows attackers to manipulate the database query through the email parameter, potentially leading to unauthorized data access or complete database compromise.',
    taint_trace: 'request.form["email"] → email → query (line 42)',
    patch_suggestion: 'Use parameterized queries: cursor.execute("SELECT * FROM users WHERE email = ?", (email,))',
  },
  {
    id: 'F-002', file: '/home/user/app/utils/subprocess_runner.py', line: 18,
    vuln_type: 'Command Injection', severity: 'critical',
    description: 'Unsanitized user input passed to subprocess.run() via shell=True.',
    snippet: 'result = subprocess.run(f"ping {host}", shell=True)',
    ai_analysis: 'Command injection via unsanitized host parameter. Attacker can append shell metacharacters to execute arbitrary commands on the server.',
    taint_trace: 'request.args["host"] → host → subprocess.run() (line 18)',
    patch_suggestion: 'Use subprocess.run(["ping", host], shell=False) with explicit allowlist validation.',
  },
  {
    id: 'F-003', file: '/home/user/app/views/profile.py', line: 87,
    vuln_type: 'Insecure Direct Object Reference', severity: 'high',
    description: 'User ID from URL parameter used directly without authorization check.',
    snippet: 'profile = User.objects.get(id=request.GET["user_id"])',
    ai_analysis: 'IDOR vulnerability allows any authenticated user to access other users\' profiles by manipulating the user_id parameter.',
    taint_trace: 'request.GET["user_id"] → User.objects.get(id=...) (line 87)',
    patch_suggestion: 'Verify requesting user has permission to access the specified user_id before returning data.',
  },
  {
    id: 'F-004', file: '/home/user/app/templates/search.html', line: 23,
    vuln_type: 'Cross-Site Scripting (XSS)', severity: 'high',
    description: 'User input rendered directly in HTML without escaping via |safe filter.',
    snippet: '<div>Results for: {{ query|safe }}</div>',
    ai_analysis: 'The |safe filter disables Django auto-escaping. An attacker can inject arbitrary HTML or JavaScript through the query parameter.',
    taint_trace: 'request.GET["query"] → query → template render (line 23)',
    patch_suggestion: 'Remove |safe filter. Use mark_safe() only after explicit sanitization with bleach.',
  },
  {
    id: 'F-005', file: '/home/user/app/config/settings.py', line: 5,
    vuln_type: 'Hardcoded Secret', severity: 'high',
    description: 'Secret key and database credentials hardcoded in configuration file.',
    snippet: 'SECRET_KEY = "django-insecure-abc123xyz"',
    ai_analysis: 'Hardcoded secrets committed to source control expose credentials to anyone with repository access, including CI/CD systems.',
    taint_trace: 'settings.py:5 → SECRET_KEY (static)',
    patch_suggestion: 'Move secrets to environment variables. Use python-dotenv or a secrets manager.',
  },
  {
    id: 'F-006', file: '/home/user/app/api/uploads.py', line: 55,
    vuln_type: 'Unrestricted File Upload', severity: 'medium',
    description: 'File upload endpoint does not validate file type or content.',
    snippet: 'file.save(os.path.join(UPLOAD_DIR, file.filename))',
    ai_analysis: 'Without extension/MIME type validation, attackers can upload executable files that may be served and interpreted by the server.',
    taint_trace: 'request.files["file"] → file.save() (line 55)',
    patch_suggestion: 'Validate extension and MIME type against an allowlist. Store uploads outside webroot.',
  },
  {
    id: 'F-007', file: '/home/user/app/middleware/csrf.py', line: 12,
    vuln_type: 'CSRF Token Missing', severity: 'medium',
    description: 'State-changing POST endpoint lacks CSRF token validation.',
    snippet: '@app.route("/transfer", methods=["POST"])\n@login_required\ndef transfer():',
    ai_analysis: 'Without CSRF protection, authenticated users can be tricked into making unintended state changes via forged cross-site requests.',
    taint_trace: '/transfer endpoint → no CSRF validation',
    patch_suggestion: 'Add @csrf_protect decorator or use Flask-WTF for automatic CSRF protection.',
  },
  {
    id: 'F-008', file: '/home/user/app/auth/session.py', line: 33,
    vuln_type: 'Weak Session Configuration', severity: 'medium',
    description: 'Session cookies missing Secure and HttpOnly flags.',
    snippet: 'app.config["SESSION_COOKIE_SECURE"] = False',
    ai_analysis: 'Without the Secure flag, session cookies may be sent over plain HTTP. Without HttpOnly, they are accessible to JavaScript, enabling XSS-based session theft.',
    taint_trace: 'session.py:33 → SESSION_COOKIE_SECURE = False',
    patch_suggestion: 'Set SESSION_COOKIE_SECURE=True and SESSION_COOKIE_HTTPONLY=True in all environments.',
  },
  {
    id: 'F-009', file: '/home/user/app/utils/logger.py', line: 78,
    vuln_type: 'Sensitive Data Exposure', severity: 'low',
    description: 'Passwords logged in plaintext during authentication.',
    snippet: 'logger.debug(f"Auth attempt: user={username}, pass={password}")',
    ai_analysis: 'Passwords appearing in logs can be exposed to log aggregation services, sysadmins, or anyone with log access, violating data protection requirements.',
    taint_trace: 'request.form["password"] → password → logger.debug() (line 78)',
    patch_suggestion: 'Never log credentials. Use logger.debug("Auth attempt: user=%s", username) only.',
  },
  {
    id: 'F-010', file: '/home/user/app/views/admin.py', line: 101,
    vuln_type: 'Missing Authorization', severity: 'critical',
    description: 'Admin endpoint accessible to all authenticated users without role check.',
    snippet: '@login_required\ndef admin_panel():\n    return render_admin()',
    ai_analysis: 'Any logged-in user can access admin functionality. Role-based access control is completely absent, enabling privilege escalation.',
    taint_trace: '/admin endpoint → @login_required only, no role check',
    patch_suggestion: 'Add @require_role("admin") decorator and verify user.is_staff server-side.',
  },
  {
    id: 'F-011', file: '/home/user/app/deps/requirements.txt', line: 4,
    vuln_type: 'Vulnerable Dependency', severity: 'high',
    description: 'Django 3.1.2 has known CVE-2021-31542 (directory traversal).',
    snippet: 'Django==3.1.2',
    ai_analysis: 'Outdated Django version contains a path traversal vulnerability in FileField and ImageField that allows reading arbitrary files from the server.',
    taint_trace: 'requirements.txt:4 → Django==3.1.2 (CVE-2021-31542)',
    patch_suggestion: 'Upgrade to Django >= 3.2.7 or the current stable release.',
  },
  {
    id: 'F-012', file: '/home/user/app/api/graphql.py', line: 29,
    vuln_type: 'GraphQL Introspection Enabled', severity: 'low',
    description: 'GraphQL introspection is enabled in production, allowing full schema discovery.',
    snippet: 'app.add_url_rule("/graphql", view_func=GraphQLView.as_view(graphiql=True))',
    ai_analysis: 'Schema exposure helps attackers enumerate all available queries and mutations to plan targeted attacks against the API.',
    taint_trace: 'graphql.py:29 → graphiql=True',
    patch_suggestion: 'Disable introspection in production: GRAPHQL_INTROSPECTION = False.',
  },
];

const SEVERITIES = ['All', 'Critical', 'High', 'Medium', 'Low'] as const;
type SevFilter = (typeof SEVERITIES)[number];

const PAGE_SIZE = 8;

export default function FindingsPage() {
  const [search, setSearch] = useState('');
  const [severity, setSeverity] = useState<SevFilter>('All');
  const [selected, setSelected] = useState<Finding | null>(null);
  const [page, setPage] = useState(1);

  const filtered = useMemo(() => {
    const q = search.toLowerCase();
    return MOCK_FINDINGS.filter((f) => {
      const matchSev = severity === 'All' || f.severity.toLowerCase() === severity.toLowerCase();
      const matchSearch =
        !q ||
        f.vuln_type.toLowerCase().includes(q) ||
        f.file.toLowerCase().includes(q) ||
        f.description.toLowerCase().includes(q) ||
        f.id.toLowerCase().includes(q);
      return matchSev && matchSearch;
    });
  }, [search, severity]);

  const totalPages = Math.max(1, Math.ceil(filtered.length / PAGE_SIZE));
  const currentPage = Math.min(page, totalPages);
  const paged = filtered.slice((currentPage - 1) * PAGE_SIZE, currentPage * PAGE_SIZE);

  const handleSevChange = (s: SevFilter) => {
    setSeverity(s);
    setPage(1);
  };

  const handleSearch = (val: string) => {
    setSearch(val);
    setPage(1);
  };

  return (
    <div className="page-content">
      <div className="page-header">
        <div>
          <h1 className="page-title">Findings Explorer</h1>
          <p className="page-subtitle">
            {filtered.length} finding{filtered.length !== 1 ? 's' : ''} across all scans
          </p>
        </div>
        <button className="btn btn-outline">
          <DownloadIcon size={15} />
          Export
        </button>
      </div>

      <div className="findings-toolbar">
        <div className="search-bar">
          <span className="search-icon">
            <SearchIcon size={16} />
          </span>
          <input
            type="text"
            placeholder="Search by type, file, description..."
            value={search}
            onChange={(e) => handleSearch(e.target.value)}
          />
        </div>
        <div className="severity-filters" style={{ margin: 0 }}>
          {SEVERITIES.map((s) => {
            const count =
              s === 'All'
                ? MOCK_FINDINGS.length
                : MOCK_FINDINGS.filter((f) => f.severity.toLowerCase() === s.toLowerCase()).length;
            return (
              <button
                key={s}
                className={`severity-filter-btn ${s.toLowerCase()}${severity === s ? ' active' : ''}`}
                onClick={() => handleSevChange(s)}
              >
                {s}
                {count > 0 && <span style={{ opacity: 0.7, marginLeft: '0.25rem' }}>({count})</span>}
              </button>
            );
          })}
        </div>
      </div>

      <div className={`findings-layout${selected ? ' findings-layout-split' : ''}`}>
        <div className="findings-main">
          <div className="card" style={{ padding: 0, overflow: 'hidden' }}>
            {paged.length === 0 ? (
              <div className="empty-state">
                <ShieldIcon size={32} color="var(--text-muted)" />
                <p>No findings match your filters.</p>
              </div>
            ) : (
              <table className="results-table">
                <thead>
                  <tr>
                    <th>ID</th>
                    <th>Vulnerability</th>
                    <th>Severity</th>
                    <th>File</th>
                    <th>Line</th>
                  </tr>
                </thead>
                <tbody>
                  {paged.map((f) => (
                    <tr
                      key={f.id}
                      className={`clickable-row${selected?.id === f.id ? ' row-selected' : ''}`}
                      onClick={() => setSelected(selected?.id === f.id ? null : f)}
                    >
                      <td>
                        <span style={{ fontFamily: 'monospace', fontSize: '0.8rem', color: 'var(--text-muted)' }}>
                          {f.id}
                        </span>
                      </td>
                      <td>
                        <div style={{ fontWeight: 600, fontSize: '0.9rem' }}>{f.vuln_type}</div>
                      </td>
                      <td>
                        <span className={`severity ${f.severity.toLowerCase()}`}>{f.severity}</span>
                      </td>
                      <td>
                        <div style={{ fontSize: '0.875rem', fontFamily: 'monospace', color: 'var(--text-muted)' }}>
                          {f.file.split('/').pop()}
                        </div>
                      </td>
                      <td style={{ fontSize: '0.875rem', color: 'var(--text-muted)' }}>
                        {f.line}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>

          {totalPages > 1 && (
            <div className="pagination">
              <button
                className="pagination-btn"
                disabled={currentPage === 1}
                onClick={() => setPage(currentPage - 1)}
                aria-label="Previous page"
              >
                <ChevronLeftIcon size={16} />
              </button>

              {Array.from({ length: totalPages }, (_, i) => i + 1).map((p) => (
                <button
                  key={p}
                  className={`pagination-btn${p === currentPage ? ' active' : ''}`}
                  onClick={() => setPage(p)}
                >
                  {p}
                </button>
              ))}

              <button
                className="pagination-btn"
                disabled={currentPage === totalPages}
                onClick={() => setPage(currentPage + 1)}
                aria-label="Next page"
              >
                <ChevronRightIcon size={16} />
              </button>

              <span className="pagination-info">
                {(currentPage - 1) * PAGE_SIZE + 1}–{Math.min(currentPage * PAGE_SIZE, filtered.length)} of {filtered.length}
              </span>
            </div>
          )}
        </div>

        {selected && (
          <div className="card findings-detail-panel">
            <div className="detail-header">
              <div style={{ minWidth: 0 }}>
                <div style={{ fontWeight: 700, fontSize: '0.95rem' }}>{selected.vuln_type}</div>
                <span className={`severity ${selected.severity.toLowerCase()}`} style={{ marginTop: '0.375rem', display: 'inline-block' }}>
                  {selected.severity}
                </span>
              </div>
              <button className="btn-icon" onClick={() => setSelected(null)} aria-label="Close">
                <XIcon size={16} />
              </button>
            </div>

            <div className="detail-section">
              <h4>Location</h4>
              <div style={{ display: 'flex', alignItems: 'center', gap: '0.5rem' }}>
                <FileIcon size={14} color="var(--text-muted)" />
                <span style={{ fontFamily: 'monospace', fontSize: '0.8rem', color: 'var(--text-muted)' }}>
                  {selected.file}:{selected.line}
                </span>
              </div>
            </div>

            <div className="detail-section">
              <h4>Description</h4>
              <p style={{ fontSize: '0.875rem', color: 'var(--text-color)', lineHeight: 1.6 }}>
                {selected.description}
              </p>
            </div>

            <div className="detail-section">
              <h4>Code Snippet</h4>
              <div className="snippet">{selected.snippet}</div>
            </div>

            {selected.ai_analysis && (
              <div className="detail-section">
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
                  {selected.ai_analysis}
                </p>
              </div>
            )}

            {selected.taint_trace && (
              <div className="detail-section">
                <h4>Taint Trace</h4>
                <div className="taint-trace">{selected.taint_trace}</div>
              </div>
            )}

            {selected.patch_suggestion && (
              <div className="detail-section">
                <h4>Patch Suggestion</h4>
                <p style={{
                  fontSize: '0.875rem',
                  color: 'var(--text-color)',
                  lineHeight: 1.6,
                  background: 'rgba(16,185,129,0.06)',
                  padding: '0.75rem',
                  borderRadius: '0.375rem',
                  borderLeft: '2px solid var(--success-color)',
                }}>
                  {selected.patch_suggestion}
                </p>
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
