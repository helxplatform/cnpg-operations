#!/usr/bin/env python3
"""
Grant a deployer ServiceAccount the RBAC permissions needed to
helm-install the cnpg-operations chart.

Problem
-------
The Helm chart creates RoleBindings that reference CNPG operator
ClusterRoles.  Kubernetes requires that the deployer SA already holds
every permission in those ClusterRoles before it can create such
bindings.  This script, run once by a cluster-admin, bridges that gap.

CNPG operator ClusterRoles (created by the operator Helm chart):
  cnpg-cloudnative-pg       — full operator permissions
  cnpg-cloudnative-pg-edit  — CRD create/update/delete
  cnpg-cloudnative-pg-view  — CRD read-only

Usage
-----
  # Grant (must be run by a cluster-admin):
  python bin/grant-helm-rbac.py --sa claude --sa-namespace claude

  # Different target namespace for secrets Role:
  python bin/grant-helm-rbac.py --sa claude --sa-namespace claude --namespace cnpg

  # Dry run:
  python bin/grant-helm-rbac.py --sa claude --sa-namespace claude --dry-run

  # Remove the grants:
  python bin/grant-helm-rbac.py --sa claude --sa-namespace claude --delete

  # Custom CNPG roles (e.g. different operator release name):
  python bin/grant-helm-rbac.py --sa claude --sa-namespace claude \\
      --cnpg-roles my-cnpg-cloudnative-pg,my-cnpg-cloudnative-pg-edit
"""

import argparse
import sys

from kubernetes import client, config
from kubernetes.client.rest import ApiException


LABEL_KEY = "managed-by"
LABEL_VALUE = "grant-helm-rbac"
DEFAULT_CNPG_ROLES = ["cnpg-cloudnative-pg", "cnpg-cloudnative-pg-edit"]

# V1Subject was renamed to RbacV1Subject in older kubernetes client versions.
Subject = getattr(client, "V1Subject", None) or client.RbacV1Subject


# ── Kubernetes helpers ───────────────────────────────────────────────────────

def init_clients():
    """Load kubeconfig / in-cluster config and return (RbacV1Api, CoreV1Api)."""
    try:
        config.load_incluster_config()
        print("Loaded in-cluster config")
    except config.ConfigException:
        try:
            config.load_kube_config()
            print("Loaded kubeconfig from file")
        except Exception as e:
            print(f"Failed to load Kubernetes config: {e}", file=sys.stderr)
            raise
    return client.RbacAuthorizationV1Api(), client.CoreV1Api()


def resource_exists(fn, *args):
    """Return True if a read call succeeds, False on 404, re-raise otherwise."""
    try:
        fn(*args)
        return True
    except ApiException as e:
        if e.status == 404:
            return False
        raise


def current_namespace():
    """Best-effort namespace from current kubeconfig context."""
    try:
        _, ctx = config.list_kube_config_contexts()
        return (ctx or {}).get("context", {}).get("namespace") or "default"
    except Exception:
        return "default"


# ── Create / delete helpers ──────────────────────────────────────────────────

def ensure_cluster_role_binding(rbac, name, cluster_role, sa_name, sa_ns, dry_run):
    """Create a ClusterRoleBinding (idempotent)."""
    if dry_run:
        print(f"  [dry-run] Would create ClusterRoleBinding {name} -> {cluster_role}")
        return True

    if resource_exists(rbac.read_cluster_role_binding, name):
        print(f"  ClusterRoleBinding {name} already exists")
        return True

    body = client.V1ClusterRoleBinding(
        metadata=client.V1ObjectMeta(
            name=name,
            labels={LABEL_KEY: LABEL_VALUE},
        ),
        role_ref=client.V1RoleRef(
            api_group="rbac.authorization.k8s.io",
            kind="ClusterRole",
            name=cluster_role,
        ),
        subjects=[
            Subject(
                kind="ServiceAccount",
                name=sa_name,
                namespace=sa_ns,
            )
        ],
    )
    try:
        rbac.create_cluster_role_binding(body)
        print(f"  Created ClusterRoleBinding {name} -> {cluster_role}")
        return True
    except ApiException as e:
        print(f"  Failed to create ClusterRoleBinding {name}: {e.reason}",
              file=sys.stderr)
        return False


def ensure_secrets_role(rbac, role_name, namespace, dry_run):
    """Create a Role granting secrets CRUD (idempotent)."""
    if dry_run:
        print(f"  [dry-run] Would create Role {namespace}/{role_name}")
        return True

    body = client.V1Role(
        metadata=client.V1ObjectMeta(
            name=role_name,
            namespace=namespace,
            labels={LABEL_KEY: LABEL_VALUE},
        ),
        rules=[
            client.V1PolicyRule(
                api_groups=[""],
                resources=["secrets"],
                verbs=["get", "list", "watch", "create", "update", "patch", "delete"],
            )
        ],
    )
    try:
        if resource_exists(rbac.read_namespaced_role, role_name, namespace):
            rbac.replace_namespaced_role(role_name, namespace, body)
            print(f"  Updated Role {namespace}/{role_name}")
        else:
            rbac.create_namespaced_role(namespace, body)
            print(f"  Created Role {namespace}/{role_name}")
        return True
    except ApiException as e:
        print(f"  Failed to create Role {role_name}: {e.reason}", file=sys.stderr)
        return False


def ensure_role_binding(rbac, name, role_name, namespace, sa_name, sa_ns, dry_run):
    """Create a RoleBinding (idempotent)."""
    if dry_run:
        print(f"  [dry-run] Would create RoleBinding {namespace}/{name}")
        return True

    if resource_exists(rbac.read_namespaced_role_binding, name, namespace):
        print(f"  RoleBinding {namespace}/{name} already exists")
        return True

    body = client.V1RoleBinding(
        metadata=client.V1ObjectMeta(
            name=name,
            namespace=namespace,
            labels={LABEL_KEY: LABEL_VALUE},
        ),
        role_ref=client.V1RoleRef(
            api_group="rbac.authorization.k8s.io",
            kind="Role",
            name=role_name,
        ),
        subjects=[
            Subject(
                kind="ServiceAccount",
                name=sa_name,
                namespace=sa_ns,
            )
        ],
    )
    try:
        rbac.create_namespaced_role_binding(namespace, body)
        print(f"  Created RoleBinding {namespace}/{name}")
        return True
    except ApiException as e:
        print(f"  Failed to create RoleBinding {name}: {e.reason}", file=sys.stderr)
        return False


def delete_ignore_missing(fn, *args):
    """Call a delete function; treat 404 as success."""
    try:
        fn(*args)
        return True
    except ApiException as e:
        if e.status == 404:
            return True
        print(f"  Delete failed: {e.reason}", file=sys.stderr)
        return False


# ── Main actions ─────────────────────────────────────────────────────────────

def grant(rbac, cnpg_roles, sa_name, sa_ns, target_ns, dry_run):
    """Create all bindings."""
    ok = True

    # Verify CNPG ClusterRoles exist
    for role in cnpg_roles:
        if not resource_exists(rbac.read_cluster_role, role):
            print(f"ERROR: ClusterRole '{role}' not found.", file=sys.stderr)
            print("Is the CloudNativePG operator installed?", file=sys.stderr)
            return False
        print(f"  Found ClusterRole: {role}")

    print()

    # Bind deployer SA to each CNPG ClusterRole
    for role in cnpg_roles:
        name = f"{sa_name}-{role}"
        if not ensure_cluster_role_binding(rbac, name, role, sa_name, sa_ns, dry_run):
            ok = False

    # Secrets Role + RoleBinding in the target namespace
    print()
    secrets_role = f"{sa_name}-secrets-{target_ns}"
    if not ensure_secrets_role(rbac, secrets_role, target_ns, dry_run):
        ok = False
    if not ensure_role_binding(rbac, secrets_role, secrets_role, target_ns,
                               sa_name, sa_ns, dry_run):
        ok = False

    return ok


def revoke(rbac, cnpg_roles, sa_name, sa_ns, target_ns, dry_run):
    """Remove all bindings created by grant."""
    ok = True

    for role in cnpg_roles:
        name = f"{sa_name}-{role}"
        if dry_run:
            print(f"  [dry-run] Would delete ClusterRoleBinding {name}")
        else:
            print(f"  Deleting ClusterRoleBinding {name}")
            if not delete_ignore_missing(rbac.delete_cluster_role_binding, name):
                ok = False

    secrets_role = f"{sa_name}-secrets-{target_ns}"
    if dry_run:
        print(f"  [dry-run] Would delete RoleBinding {target_ns}/{secrets_role}")
        print(f"  [dry-run] Would delete Role {target_ns}/{secrets_role}")
    else:
        print(f"  Deleting RoleBinding {target_ns}/{secrets_role}")
        if not delete_ignore_missing(
                rbac.delete_namespaced_role_binding, secrets_role, target_ns):
            ok = False
        print(f"  Deleting Role {target_ns}/{secrets_role}")
        if not delete_ignore_missing(
                rbac.delete_namespaced_role, secrets_role, target_ns):
            ok = False

    return ok


# ── CLI ──────────────────────────────────────────────────────────────────────

def parse_args():
    cur_ns = current_namespace()
    p = argparse.ArgumentParser(
        description="Grant a deployer SA the RBAC needed to helm-install cnpg-operations.",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog=f"""\
examples:
  # Grant (run as cluster-admin):
  python bin/grant-helm-rbac.py --sa claude --sa-namespace claude

  # Target a different namespace for secrets:
  python bin/grant-helm-rbac.py --sa claude --sa-namespace claude --namespace prod

  # Dry run:
  python bin/grant-helm-rbac.py --sa claude --sa-namespace claude --dry-run

  # Remove grants:
  python bin/grant-helm-rbac.py --sa claude --sa-namespace claude --delete

current kubeconfig namespace: {cur_ns}
""",
    )
    p.add_argument("--sa", required=True, help="Deployer ServiceAccount name")
    p.add_argument("--sa-namespace", required=True,
                   help="Namespace of the deployer ServiceAccount")
    p.add_argument("--namespace", default=None,
                   help="Target namespace for secrets Role (default: --sa-namespace)")
    p.add_argument("--cnpg-roles", default=",".join(DEFAULT_CNPG_ROLES),
                   help="Comma-separated CNPG ClusterRoles (default: %(default)s)")
    p.add_argument("--dry-run", action="store_true",
                   help="Show what would be created/deleted")
    p.add_argument("--delete", action="store_true",
                   help="Remove previously created bindings")
    return p.parse_args()


def main():
    args = parse_args()
    target_ns = args.namespace or args.sa_namespace
    cnpg_roles = [r.strip() for r in args.cnpg_roles.split(",") if r.strip()]

    print("=" * 66)
    print("grant-helm-rbac: RBAC for cnpg-operations Helm deploys")
    print("=" * 66)
    print(f"  Deployer SA:       {args.sa_namespace}/{args.sa}")
    print(f"  Target namespace:  {target_ns}")
    print(f"  CNPG ClusterRoles: {', '.join(cnpg_roles)}")
    print(f"  Dry run:           {args.dry_run}")
    print(f"  Action:            {'delete' if args.delete else 'grant'}")
    print()

    rbac, _ = init_clients()

    if args.delete:
        ok = revoke(rbac, cnpg_roles, args.sa, args.sa_namespace,
                     target_ns, args.dry_run)
    else:
        ok = grant(rbac, cnpg_roles, args.sa, args.sa_namespace,
                    target_ns, args.dry_run)

    print()
    print("=" * 66)
    if ok:
        if args.delete:
            print("Grants removed.")
        else:
            print(f"Done. {args.sa_namespace}/{args.sa} can now deploy "
                  f"cnpg-operations via Helm into namespace {target_ns}.")
            print()
            print(f"  helm install <release> cnpg-operations-*.tgz -n {target_ns}")
    else:
        print("Completed with errors — check messages above.", file=sys.stderr)
    print("=" * 66)

    sys.exit(0 if ok else 1)


if __name__ == "__main__":
    main()
