package com.authzed.spicedb;

import com.authzed.spicedb.errors.LocalValidationException;
import java.util.*;

final class JsonContext {
  static void required(String value, String path) {
    if (value == null || value.isEmpty())
      throw new LocalValidationException(path + " must not be empty");
  }

  static Map<String, Object> snapshot(Map<String, Object> value) {
    return value == null ? null : map(value, "context", new IdentityHashMap<>());
  }

  static Map<String, Object> map(Map<?, ?> m, String path, IdentityHashMap<Object, Boolean> seen) {
    if (seen.put(m, true) != null) throw new LocalValidationException(path + ": cyclic context");
    try {
      var copy = new LinkedHashMap<String, Object>();
      for (var e : m.entrySet()) {
        if (!(e.getKey() instanceof String key))
          throw new LocalValidationException(path + ": non-string key");
        copy.put(key, copy(e.getValue(), path + "." + key, seen));
      }
      return Collections.unmodifiableMap(copy);
    } finally {
      seen.remove(m);
    }
  }

  static Object copy(Object v, String path, IdentityHashMap<Object, Boolean> seen) {
    if (v == null || v instanceof String || v instanceof Boolean) return v;
    if (v instanceof Number n) {
      double d = n.doubleValue();
      if (!Double.isFinite(d)) throw new LocalValidationException(path + ": number is not finite");
      if (!(n instanceof Double
          || n instanceof Float
          || n instanceof Byte
          || n instanceof Short
          || n instanceof Integer
          || n instanceof Long
          || n instanceof java.math.BigInteger
          || n instanceof java.math.BigDecimal))
        throw new LocalValidationException(path + ": unsupported number");
      if (n instanceof Long
          || n instanceof java.math.BigInteger
          || n instanceof java.math.BigDecimal) {
        try {
          if (new java.math.BigDecimal(n.toString()).compareTo(java.math.BigDecimal.valueOf(d))
              != 0) throw new LocalValidationException(path + ": number cannot be represented");
        } catch (NumberFormatException e) {
          throw new LocalValidationException(path + ": invalid number", e);
        }
      }
      return n;
    }
    if (v instanceof Map<?, ?> m) return map(m, path, seen);
    if (v instanceof List<?> list) {
      if (seen.put(list, true) != null)
        throw new LocalValidationException(path + ": cyclic context");
      try {
        var copy = new ArrayList<Object>();
        for (int i = 0; i < list.size(); i++)
          copy.add(copy(list.get(i), path + "[" + i + "]", seen));
        return Collections.unmodifiableList(copy);
      } finally {
        seen.remove(list);
      }
    }
    throw new LocalValidationException(path + ": unsupported value " + v.getClass().getName());
  }
}
