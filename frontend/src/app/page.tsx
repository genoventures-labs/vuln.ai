'use client';

import Link from 'next/link';
import {
  ScanIcon,
  TargetIcon,
  ShieldIcon,
  PlusIcon,
  AlertTriangleIcon,
  ClockIcon,
  ActivityIcon,
} from '@/components/icons';

const MOCK_RECENT_SCANS = [
  { id: 'scan-001', path: '/home/user/webapp/auth', findings: 3, severity: 'critical', time: '2 min ago' },
  { id: 'scan-002', path: '/home/user/api/v2', findings: 1, severity: 'medium', time: '18 min ago' },
  { id: 'scan-003', path: '/var/www/html/backend', findings: 7, severity: 'high', time: '1 hr ago' },
  { id: 'scan-004', path: '/home/user/microservice', findings: 0, severity: 'low', time: '3 hrs ago' },
  { id: 'scan-005', path: '/home/user/libs/crypto', findings: 2, severity: 'high', time: '5 hrs ago' },
];

const MOCK_RECENT_MISSIONS = [
  { id: 'mis-001', target: 'http://target.local:8080', status: 'Resolved', time: '1 hr ago' },
  { id: 'mis-002', target: 'http://api.internal/v2', status: 'Exploited', time: '2 hrs ago' },
  { id: 'mis-003', target: 'http://staging.example.com', status: 'Patched', time: '4 hrs ago' },
  { id: 'mis-004', target: 'http://legacy.server:3000', status: 'Failed', time: '6 hrs ago' },
  { id: 'mis-005', target: 'http://new.target:80', status: 'Pending', time: '8 hrs ago' },
];

export default function DashboardPage() {
  return (
    <div className="page-content">
      <div className="page-header">
        <div>
          <h1 className="page-title">Dashboard</h1>
          <p className="page-subtitle">Security operations overview</p>
        </div>
      </div>

      <div className="stats-grid">
        <div className="stat-card">
          <div className="stat-icon stat-icon-blue">
            <ScanIcon size={22} />
          </div>
          <div className="stat-info">
            <div className="stat-value">24</div>
            <div className="stat-label">Total Scans</div>
          </div>
        </div>
        <div className="stat-card">
          <div className="stat-icon stat-icon-yellow">
            <AlertTriangleIcon size={22} />
          </div>
          <div className="stat-info">
            <div className="stat-value">18</div>
            <div className="stat-label">Open Findings</div>
          </div>
        </div>
        <div className="stat-card">
          <div className="stat-icon stat-icon-red">
            <ShieldIcon size={22} />
          </div>
          <div className="stat-info">
            <div className="stat-value">4</div>
            <div className="stat-label">Critical Findings</div>
          </div>
        </div>
        <div className="stat-card">
          <div className="stat-icon stat-icon-purple">
            <TargetIcon size={22} />
          </div>
          <div className="stat-info">
            <div className="stat-value">7</div>
            <div className="stat-label">Missions Run</div>
          </div>
        </div>
      </div>

      <div className="quick-actions">
        <Link href="/scanner" className="btn btn-primary">
          <PlusIcon size={16} />
          New Scan
        </Link>
        <Link href="/missions" className="btn btn-outline">
          <TargetIcon size={16} />
          Launch Mission
        </Link>
        <Link href="/findings" className="btn btn-ghost">
          <ActivityIcon size={16} />
          View Findings
        </Link>
      </div>

      <div className="activity-grid">
        <div className="card">
          <h2 className="card-title">Recent Scans</h2>
          <div className="activity-list">
            {MOCK_RECENT_SCANS.map((scan) => (
              <div key={scan.id} className="activity-item">
                <div className="activity-icon">
                  <ScanIcon size={14} />
                </div>
                <div className="activity-body">
                  <div className="activity-main">{scan.path.split('/').pop()}</div>
                  <div className="activity-sub">{scan.path}</div>
                </div>
                <div className="activity-meta">
                  {scan.findings > 0 ? (
                    <span className={`severity ${scan.severity}`}>
                      {scan.findings} finding{scan.findings !== 1 ? 's' : ''}
                    </span>
                  ) : (
                    <span className="severity low">Clean</span>
                  )}
                  <span className="activity-time">
                    <ClockIcon size={11} />
                    {scan.time}
                  </span>
                </div>
              </div>
            ))}
          </div>
        </div>

        <div className="card">
          <h2 className="card-title">Recent Missions</h2>
          <div className="activity-list">
            {MOCK_RECENT_MISSIONS.map((mission) => (
              <div key={mission.id} className="activity-item">
                <div className="activity-icon">
                  <TargetIcon size={14} />
                </div>
                <div className="activity-body">
                  <div className="activity-main">{mission.target}</div>
                  <div className="activity-sub">{mission.id}</div>
                </div>
                <div className="activity-meta">
                  <span className={`mission-status mission-status-${mission.status.toLowerCase()}`}>
                    {mission.status}
                  </span>
                  <span className="activity-time">
                    <ClockIcon size={11} />
                    {mission.time}
                  </span>
                </div>
              </div>
            ))}
          </div>
        </div>
      </div>
    </div>
  );
}
