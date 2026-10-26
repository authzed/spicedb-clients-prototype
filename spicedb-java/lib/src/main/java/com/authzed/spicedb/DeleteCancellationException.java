package com.authzed.spicedb;

public final class DeleteCancellationException extends java.util.concurrent.CancellationException {
  private final DeleteProgress progress;

  public DeleteCancellationException(Throwable cause, DeleteProgress progress) {
    super(cause.getMessage());
    initCause(cause);
    this.progress = progress;
  }

  public DeleteProgress deleteProgress() {
    return progress;
  }
}
