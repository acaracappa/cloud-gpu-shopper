#!/usr/bin/env python3
"""
Dynamic Ansible inventory script for GPU Shopper benchmark nodes.

Queries the GPU Shopper API for active sessions and generates
Ansible inventory from the running instances.

Usage:
    ./dynamic.py --list              # List all hosts
    ./dynamic.py --host <hostname>   # Get host variables

Environment variables:
    GPU_SHOPPER_URL   - GPU Shopper API URL (default: http://localhost:8080)
    SSH_KEY_PATH      - Path to SSH private key (default: ~/.ssh/id_rsa)
    SSH_USER          - SSH username (default: root)
"""

import argparse
import json
import os
import sys
import urllib.request
import urllib.error


def get_env(key: str, default: str) -> str:
    """Get environment variable with default."""
    return os.environ.get(key, default)


def fetch_sessions(api_url: str) -> list:
    """Fetch active sessions from GPU Shopper API."""
    url = f"{api_url}/api/v1/sessions?status=running"

    try:
        req = urllib.request.Request(url)
        req.add_header("Accept", "application/json")

        with urllib.request.urlopen(req, timeout=10) as response:
            data = json.loads(response.read().decode())
            return data.get("sessions", [])
    except urllib.error.URLError as e:
        print(f"Warning: Could not connect to GPU Shopper API: {e}", file=sys.stderr)
        return []
    except json.JSONDecodeError as e:
        print(f"Warning: Invalid JSON response: {e}", file=sys.stderr)
        return []


def build_inventory(sessions: list, ssh_key: str, ssh_user: str) -> dict:
    """Build Ansible inventory from sessions."""
    inventory = {
        "benchmark_nodes": {
            "hosts": [],
            "vars": {
                "ansible_user": ssh_user,
                "ansible_ssh_private_key_file": ssh_key,
                "ansible_ssh_common_args": "-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null",
            },
        },
        "_meta": {
            "hostvars": {},
        },
    }

    for session in sessions:
        if not session.get("ssh_host"):
            continue

        host = session["ssh_host"]
        port = session.get("ssh_port", 22)

        # Use IP:port format for unique identification
        host_id = f"{host}_{port}" if port != 22 else host

        inventory["benchmark_nodes"]["hosts"].append(host_id)
        inventory["_meta"]["hostvars"][host_id] = {
            "ansible_host": host,
            "ansible_port": port,
            "session_id": session.get("id", ""),
            "provider": session.get("provider", ""),
            "gpu_type": session.get("gpu_type", ""),
            "gpu_count": session.get("gpu_count", 1),
            "price_per_hour": session.get("price_per_hour", 0),
            "offer_id": session.get("offer_id", ""),
            "consumer_id": session.get("consumer_id", ""),
        }

    return inventory


def get_host_vars(hostname: str, sessions: list) -> dict:
    """Get variables for a specific host."""
    for session in sessions:
        if not session.get("ssh_host"):
            continue

        host = session["ssh_host"]
        port = session.get("ssh_port", 22)
        host_id = f"{host}_{port}" if port != 22 else host

        if host_id == hostname or host == hostname:
            return {
                "ansible_host": host,
                "ansible_port": port,
                "session_id": session.get("id", ""),
                "provider": session.get("provider", ""),
                "gpu_type": session.get("gpu_type", ""),
                "gpu_count": session.get("gpu_count", 1),
                "price_per_hour": session.get("price_per_hour", 0),
                "offer_id": session.get("offer_id", ""),
                "consumer_id": session.get("consumer_id", ""),
            }

    return {}


def main():
    parser = argparse.ArgumentParser(description="Dynamic Ansible inventory for GPU Shopper")
    parser.add_argument("--list", action="store_true", help="List all hosts")
    parser.add_argument("--host", type=str, help="Get host variables")
    args = parser.parse_args()

    api_url = get_env("GPU_SHOPPER_URL", "http://localhost:8080")
    ssh_key = get_env("SSH_KEY_PATH", os.path.expanduser("~/.ssh/id_rsa"))
    ssh_user = get_env("SSH_USER", "root")

    sessions = fetch_sessions(api_url)

    if args.list:
        inventory = build_inventory(sessions, ssh_key, ssh_user)
        print(json.dumps(inventory, indent=2))
    elif args.host:
        host_vars = get_host_vars(args.host, sessions)
        print(json.dumps(host_vars, indent=2))
    else:
        # Default: list
        inventory = build_inventory(sessions, ssh_key, ssh_user)
        print(json.dumps(inventory, indent=2))


if __name__ == "__main__":
    main()
