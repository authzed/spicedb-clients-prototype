package com.authzed.spicedb;

public record ObjectRef(String objectType, String objectId) {
  public ObjectRef {
    JsonContext.required(objectType, "objectType");
    JsonContext.required(objectId, "objectId");
  }
}
