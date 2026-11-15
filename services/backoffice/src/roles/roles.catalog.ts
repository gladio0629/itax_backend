import { readFileSync } from 'fs';

export type StaffRole = 'superadmin' | 'owner' | 'manager' | 'marketer' | 'operator';
export const STAFF_ROLES: StaffRole[] = ['superadmin', 'owner', 'manager', 'marketer', 'operator'];
/** Operator navbatida 3 qismni ko'radigan yuqori rollar */
export const SENIOR_ROLES: StaffRole[] = ['superadmin', 'owner', 'manager'];

interface RoleDef { kind: 'user' | 'staff'; permissions: string[]; creatable_roles?: string[] }

/** shared/roles/roles.json ni o'qiydi — rollar va ruxsatlarning YAGONA manbasi. */
export class RolesCatalog {
  private readonly roles: Record<string, RoleDef>;
  readonly sections: string[];

  constructor(file: string) {
    const j = JSON.parse(readFileSync(file, 'utf8')) as { sections: string[]; roles: Record<string, RoleDef> };
    this.roles = j.roles;
    this.sections = j.sections;
    for (const r of STAFF_ROLES) {
      if (!this.roles[r] || this.roles[r].kind !== 'staff') throw new Error(`roles.json: staff roli yo'q: ${r}`);
    }
  }

  isStaffRole(r: unknown): r is StaffRole {
    return typeof r === 'string' && (STAFF_ROLES as string[]).includes(r);
  }

  can(role: StaffRole, permission: string): boolean {
    return this.roles[role]?.permissions.includes(permission) ?? false;
  }

  permissions(role: StaffRole): string[] {
    return [...(this.roles[role]?.permissions ?? [])];
  }

  creatable(role: StaffRole): StaffRole[] {
    return (this.roles[role]?.creatable_roles ?? []).filter((r): r is StaffRole => this.isStaffRole(r));
  }

  /** Rol ko'ra oladigan bo'limlar (read yoki write ruxsati bor) */
  sectionsOf(role: StaffRole): string[] {
    return this.sections.filter((s) => this.can(role, `${s}:read`) || this.can(role, `${s}:write`));
  }

  isSenior(role: StaffRole): boolean {
    return SENIOR_ROLES.includes(role);
  }
}

export const ROLES = Symbol('ROLES');
