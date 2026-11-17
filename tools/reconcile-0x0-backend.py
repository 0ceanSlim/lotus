#!/usr/bin/env python3
"""One-time reconcile between lotus and its 0x0 backend.

Before lotus recorded the backend's management tokens and passed its own
expiry on upload, the two drifted apart: lotus could not delete files on the
backend, and the backend kept files longer (its own 30-365 day curve) than
lotus promised (30-90 days). Both sides key files by the SHA-256 of their
bytes, so they can be matched exactly.

  1. copy each file's management token into lotus (blobs_0x0.token), so
     deletes and expiry changes reach the backend from now on;
  2. set the backend's expiration to lotus's expiry, so files leave the
     disk when lotus says they do;
  3. with --expire-orphans, mark backend files that lotus no longer lists
     (their lotus record already expired) to expire now; the backend's own
     prune job then removes them.

Dry run by default. Stop both services first, then run with --apply:

  sudo systemctl stop blossom-server-0x0 0x0
  python3 reconcile-0x0-backend.py --lotus-db ~/blossom-server-0x0/db/database.sqlite3 \\
      --backend-db ~/0x0/data/0x0.sqlite --apply [--expire-orphans]
  sudo systemctl start 0x0 blossom-server-0x0
"""
import argparse
import os
import sqlite3
import time


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--lotus-db", required=True)
    ap.add_argument("--backend-db", required=True)
    ap.add_argument("--apply", action="store_true", help="write changes (default: dry run)")
    ap.add_argument("--expire-orphans", action="store_true", help="expire backend files lotus no longer lists")
    a = ap.parse_args()

    lotus = sqlite3.connect(os.path.expanduser(a.lotus_db))
    backend = sqlite3.connect(os.path.expanduser(a.backend_db))
    cols = [r[1] for r in lotus.execute("PRAGMA table_info(blobs_0x0)")]
    if "token" not in cols:
        raise SystemExit("blobs_0x0 has no token column: start the new lotus once so it migrates, then stop it and rerun")

    now_ms = int(time.time() * 1000)
    rows = lotus.execute("SELECT hash, expiry, COALESCE(token, '') FROM blobs_0x0").fetchall()
    listed = {h: (exp, tok) for h, exp, tok in rows}
    files = backend.execute("SELECT id, sha256, expiration, mgmt_token FROM file WHERE NOT removed").fetchall()

    tokens = expiries = orphans = shortened = 0
    orphan_bytes = 0
    for fid, sha, expiration, token in files:
        if sha in listed:
            exp, have = listed[sha]
            if not have and token:
                tokens += 1
                if a.apply:
                    lotus.execute("UPDATE blobs_0x0 SET token = ? WHERE hash = ?", (token, sha))
            if exp and expiration != exp * 1000:
                expiries += 1
                if expiration and exp * 1000 < expiration:
                    shortened += 1
                if a.apply:
                    backend.execute("UPDATE file SET expiration = ? WHERE id = ?", (exp * 1000, fid))
        else:
            orphans += 1
            orphan_bytes += backend.execute("SELECT COALESCE(size, 0) FROM file WHERE id = ?", (fid,)).fetchone()[0]
            if a.apply and a.expire_orphans:
                backend.execute("UPDATE file SET expiration = ? WHERE id = ?", (now_ms, fid))

    missing = len(set(listed) - {f[1] for f in files})
    print(f"lotus lists {len(listed)} files; backend holds {len(files)}")
    print(f"  tokens recovered into lotus:        {tokens}")
    print(f"  backend expiries set to lotus's:    {expiries} ({shortened} of them earlier than before)")
    print(f"  backend files lotus no longer lists: {orphans} ({orphan_bytes / 1e9:.2f} GB)" + (" -> expire now" if a.expire_orphans else " (left alone; --expire-orphans to remove)"))
    print(f"  lotus files missing on the backend: {missing}")
    if a.apply:
        lotus.commit()
        backend.commit()
        print("applied.")
    else:
        print("dry run: nothing written. add --apply to write.")


if __name__ == "__main__":
    main()
