package main

// A v2 login inherits the existing application grants, but may not assume the
// old login's identity or any other role. Provisioning remains an admin task.
const restrictedV2RoleCheck = `
SELECT current_user = 'lykeion_app_v2' AND session_user = current_user
 AND r.rolcanlogin AND r.rolinherit AND NOT r.rolsuper AND NOT r.rolbypassrls
 AND NOT r.rolcreatedb AND NOT r.rolcreaterole AND NOT r.rolreplication
 AND EXISTS (
   SELECT 1 FROM pg_catalog.pg_roles p JOIN pg_catalog.pg_auth_members m ON m.roleid=p.oid
   WHERE p.rolname='lykeion_app' AND m.member=r.oid
     AND m.inherit_option AND NOT m.set_option AND NOT m.admin_option
     AND NOT p.rolsuper AND NOT p.rolbypassrls AND NOT p.rolcreatedb
     AND NOT p.rolcreaterole AND NOT p.rolreplication
 )
 AND NOT EXISTS (
   SELECT 1 FROM pg_catalog.pg_auth_members m JOIN pg_catalog.pg_roles p ON p.oid=m.roleid
   WHERE m.member=r.oid AND (p.rolname <> 'lykeion_app'
     OR NOT m.inherit_option OR m.set_option OR m.admin_option)
 )
 AND NOT EXISTS (
   SELECT 1 FROM pg_catalog.pg_auth_members m JOIN pg_catalog.pg_roles p ON p.oid=m.member
   WHERE p.rolname='lykeion_app'
 )
 AND NOT pg_catalog.has_database_privilege(current_user, pg_catalog.current_database(), 'CREATE')
 AND NOT EXISTS (
   SELECT 1 FROM pg_catalog.pg_namespace n
   WHERE pg_catalog.pg_has_role(current_user,n.nspowner,'MEMBER')
      OR pg_catalog.has_schema_privilege(current_user,n.oid,'CREATE')
 )
 AND NOT EXISTS (
   SELECT 1 FROM pg_catalog.pg_class c
   WHERE pg_catalog.pg_has_role(current_user,c.relowner,'MEMBER')
 )
 AND NOT EXISTS (
   SELECT 1 FROM pg_catalog.pg_proc p
   WHERE pg_catalog.pg_has_role(current_user,p.proowner,'MEMBER')
 )
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_type t WHERE pg_catalog.pg_has_role(current_user,t.typowner,'MEMBER'))
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_extension e WHERE pg_catalog.pg_has_role(current_user,e.extowner,'MEMBER'))
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_foreign_data_wrapper f WHERE pg_catalog.pg_has_role(current_user,f.fdwowner,'MEMBER'))
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_foreign_server s WHERE pg_catalog.pg_has_role(current_user,s.srvowner,'MEMBER'))
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_language l WHERE pg_catalog.pg_has_role(current_user,l.lanowner,'MEMBER'))
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_tablespace t WHERE pg_catalog.pg_has_role(current_user,t.spcowner,'MEMBER'))
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_largeobject_metadata l WHERE pg_catalog.pg_has_role(current_user,l.lomowner,'MEMBER'))
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_publication p WHERE pg_catalog.pg_has_role(current_user,p.pubowner,'MEMBER'))
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_collation c WHERE pg_catalog.pg_has_role(current_user,c.collowner,'MEMBER'))
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_conversion c WHERE pg_catalog.pg_has_role(current_user,c.conowner,'MEMBER'))
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_operator o WHERE pg_catalog.pg_has_role(current_user,o.oprowner,'MEMBER'))
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_opclass o WHERE pg_catalog.pg_has_role(current_user,o.opcowner,'MEMBER'))
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_opfamily o WHERE pg_catalog.pg_has_role(current_user,o.opfowner,'MEMBER'))
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_ts_config c WHERE pg_catalog.pg_has_role(current_user,c.cfgowner,'MEMBER'))
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_ts_dict d WHERE pg_catalog.pg_has_role(current_user,d.dictowner,'MEMBER'))
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_event_trigger e WHERE pg_catalog.pg_has_role(current_user,e.evtowner,'MEMBER'))
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_statistic_ext s WHERE pg_catalog.pg_has_role(current_user,s.stxowner,'MEMBER'))
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_default_acl d WHERE pg_catalog.pg_has_role(current_user,d.defaclrole,'MEMBER'))
 -- All privileges must be inherited from lykeion_app or PUBLIC. A direct
 -- grant could otherwise make a non-RLS internal table readable by this login.
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_class c,
   LATERAL pg_catalog.aclexplode(c.relacl) a WHERE a.grantee=r.oid)
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_attribute c,
   LATERAL pg_catalog.aclexplode(c.attacl) a WHERE a.grantee=r.oid)
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_proc p,
   LATERAL pg_catalog.aclexplode(p.proacl) a WHERE a.grantee=r.oid)
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_namespace n,
   LATERAL pg_catalog.aclexplode(n.nspacl) a WHERE a.grantee=r.oid)
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_database d,
   LATERAL pg_catalog.aclexplode(d.datacl) a WHERE a.grantee=r.oid)
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_type t,
   LATERAL pg_catalog.aclexplode(t.typacl) a WHERE a.grantee=r.oid)
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_foreign_data_wrapper f,
   LATERAL pg_catalog.aclexplode(f.fdwacl) a WHERE a.grantee=r.oid)
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_foreign_server s,
   LATERAL pg_catalog.aclexplode(s.srvacl) a WHERE a.grantee=r.oid)
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_language l,
   LATERAL pg_catalog.aclexplode(l.lanacl) a WHERE a.grantee=r.oid)
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_tablespace t,
   LATERAL pg_catalog.aclexplode(t.spcacl) a WHERE a.grantee=r.oid)
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_largeobject_metadata l,
   LATERAL pg_catalog.aclexplode(l.lomacl) a WHERE a.grantee=r.oid)
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_parameter_acl p,
   LATERAL pg_catalog.aclexplode(p.paracl) a WHERE a.grantee=r.oid)
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_default_acl d,
   LATERAL pg_catalog.aclexplode(d.defaclacl) a WHERE a.grantee=r.oid)
 FROM pg_catalog.pg_roles r WHERE r.rolname=current_user`
