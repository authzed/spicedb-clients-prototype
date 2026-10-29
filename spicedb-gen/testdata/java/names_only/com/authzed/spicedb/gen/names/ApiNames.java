package com.authzed.spicedb.gen.names;

import java.util.List;

/**
 * A shared API class that uses the --java.names-only output. The compileNamesOnly task compiles it
 * with an empty classpath, so it compiles only while the generated class needs nothing beyond the
 * JDK.
 */
public final class ApiNames {
  private ApiNames() {}

  public static final String DOCUMENT_TYPE = Permissions.Definitions.DOCUMENT;
  public static final String VIEW = Permissions.DocumentPermissions.VIEW;
  public static final String ORG_ADMIN = Permissions.OrganizationRelations.ADMIN;

  public static List<String> definitions() {
    return Permissions.Definitions.names();
  }

  public static String schema() {
    return Permissions.SCHEMA;
  }
}
