package com.authzed.spicedb;

public record ObjectRef(String objectType, String objectId) {
  public ObjectRef {
    JsonContext.required(objectType, "objectType");
    JsonContext.required(objectId, "objectId");
  }

  /** {@code type:id}, as in SpiceDB tuple syntax. */
  @Override
  public String toString() {
    return objectType + ":" + objectId;
  }
}
