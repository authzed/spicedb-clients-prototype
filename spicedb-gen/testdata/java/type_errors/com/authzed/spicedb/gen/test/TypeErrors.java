package com.authzed.spicedb.gen.test;

import static com.authzed.spicedb.Consistency.*;
import static com.authzed.spicedb.gen.test.Permissions.*;

/**
 * These calls should NOT compile because the sealed interface hierarchy
 * prevents invalid permission/subject combinations.
 */
class TypeErrors {
    void teamMemberCannotDelete(TypedClient tc) {
        // Team members cannot have delete permission on documents.
        tc.check(full(), Document("readme").delete(), Team("eng").member());
    }
    void teamMemberCannotEdit(TypedClient tc) {
        // Team members cannot have edit permission on documents.
        tc.check(full(), Document("readme").edit(), Team("eng").member());
    }
    void cannotWriteTeamMemberAsEditor(TypedClient tc) {
        // Team members cannot be assigned as editors (only users can).
        Document("readme").editor(Team("eng").member());
    }
}
