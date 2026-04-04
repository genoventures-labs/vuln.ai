'use client';

import Link from 'next/link';
import { usePathname } from 'next/navigation';
import { useState } from 'react';
import { HomeIcon, ScanIcon, TargetIcon, ShieldIcon, MenuIcon, XIcon } from './icons';

const NAV_ITEMS = [
  { href: '/', label: 'Dashboard', Icon: HomeIcon },
  { href: '/scanner', label: 'Scanner', Icon: ScanIcon },
  { href: '/missions', label: 'Missions', Icon: TargetIcon },
  { href: '/findings', label: 'Findings', Icon: ShieldIcon },
];

export default function Sidebar() {
  const pathname = usePathname();
  const [open, setOpen] = useState(false);

  const isActive = (href: string) => {
    if (href === '/') return pathname === '/';
    return pathname.startsWith(href);
  };

  return (
    <>
      {open && (
        <div
          className="sidebar-overlay"
          onClick={() => setOpen(false)}
          aria-hidden="true"
        />
      )}

      <button
        className="sidebar-hamburger"
        onClick={() => setOpen(!open)}
        aria-label="Toggle navigation"
      >
        {open ? <XIcon size={20} /> : <MenuIcon size={20} />}
      </button>

      <aside className={`sidebar${open ? ' sidebar-open' : ''}`}>
        <div className="sidebar-logo">
          <span className="logo-text">vuln.ai</span>
        </div>

        <nav className="sidebar-nav" aria-label="Main navigation">
          {NAV_ITEMS.map(({ href, label, Icon }) => (
            <Link
              key={href}
              href={href}
              className={`sidebar-nav-item${isActive(href) ? ' active' : ''}`}
              onClick={() => setOpen(false)}
            >
              <Icon size={18} />
              <span>{label}</span>
            </Link>
          ))}
        </nav>

        <div className="sidebar-footer">
          <div className="sidebar-status">
            <span className="status-dot status-connected" />
            <span>ORI Connected</span>
          </div>
        </div>
      </aside>
    </>
  );
}
