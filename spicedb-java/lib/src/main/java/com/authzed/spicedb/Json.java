package com.authzed.spicedb;

import com.authzed.spicedb.errors.InvalidArgumentException;
import com.authzed.spicedb.errors.LocalValidationException;
import java.time.Instant;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.TreeMap;

/**
 * Minimal JSON reader/writer for diagnostic forms and SpiceDB tuple caveat contexts. Kept internal
 * so the library adds no JSON dependency; values use the same Java types as caveat contexts
 * (String-keyed maps, lists, strings, booleans, numbers as Double, null).
 */
final class Json {
  private Json() {}

  /** Compact JSON with keys in insertion order (diagnostic {@code toJson()} forms). */
  static String write(Object value) {
    var sb = new StringBuilder();
    write(sb, value, false);
    return sb.toString();
  }

  /**
   * SpiceDB's canonical caveat-context form, as its tuple formatter prints it: keys sorted, members
   * and list items separated by {@code ", "}.
   */
  static String writeCanonical(Object value) {
    var sb = new StringBuilder();
    write(sb, value, true);
    return sb.toString();
  }

  private static void write(StringBuilder sb, Object value, boolean canonical) {
    String sep = canonical ? ", " : ",";
    if (value == null) {
      sb.append("null");
    } else if (value instanceof String s) {
      quote(sb, s);
    } else if (value instanceof Boolean b) {
      sb.append(b);
    } else if (value instanceof Number n) {
      double d = n.doubleValue();
      if (d == Math.rint(d) && !Double.isInfinite(d) && Math.abs(d) < 1e15) {
        sb.append((long) d);
      } else {
        sb.append(n instanceof Double || n instanceof Float ? Double.toString(d) : n.toString());
      }
    } else if (value instanceof Map<?, ?> m) {
      Map<?, ?> entries = canonical ? new TreeMap<>(stringKeys(m)) : m;
      sb.append('{');
      boolean first = true;
      for (var e : entries.entrySet()) {
        if (!first) sb.append(sep);
        first = false;
        quote(sb, String.valueOf(e.getKey()));
        sb.append(':');
        write(sb, e.getValue(), canonical);
      }
      sb.append('}');
    } else if (value instanceof List<?> l) {
      sb.append('[');
      for (int i = 0; i < l.size(); i++) {
        if (i > 0) sb.append(sep);
        write(sb, l.get(i), canonical);
      }
      sb.append(']');
    } else if (value instanceof Instant i) {
      quote(sb, i.toString());
    } else if (value instanceof JsonValue j) {
      write(sb, j.toJsonValue(), canonical);
    } else {
      quote(sb, value.toString());
    }
  }

  private static Map<String, Object> stringKeys(Map<?, ?> m) {
    var out = new LinkedHashMap<String, Object>();
    for (var e : m.entrySet()) out.put(String.valueOf(e.getKey()), e.getValue());
    return out;
  }

  private static void quote(StringBuilder sb, String s) {
    sb.append('"');
    for (int i = 0; i < s.length(); i++) {
      char c = s.charAt(i);
      switch (c) {
        case '"' -> sb.append("\\\"");
        case '\\' -> sb.append("\\\\");
        case '\n' -> sb.append("\\n");
        case '\r' -> sb.append("\\r");
        case '\t' -> sb.append("\\t");
        case '\b' -> sb.append("\\b");
        case '\f' -> sb.append("\\f");
        default -> {
          if (c < 0x20) sb.append(String.format("\\u%04x", (int) c));
          else sb.append(c);
        }
      }
    }
    sb.append('"');
  }

  /** Values with a structured JSON form. */
  interface JsonValue {
    Object toJsonValue();
  }

  /** A parsed value and the index just past it. */
  record Parsed(Object value, int end) {}

  /** Parses one JSON value starting at {@code start} (leading whitespace allowed). */
  static Parsed parse(String s, int start) {
    var p = new Parser(s, start);
    Object v = p.value();
    return new Parsed(v, p.pos);
  }

  private static final class Parser {
    final String s;
    int pos;

    Parser(String s, int pos) {
      this.s = s;
      this.pos = pos;
    }

    InvalidArgumentException error(String what) {
      return new LocalValidationException("invalid JSON at offset " + pos + ": " + what);
    }

    void ws() {
      while (pos < s.length() && Character.isWhitespace(s.charAt(pos))) pos++;
    }

    char peek() {
      ws();
      if (pos >= s.length()) throw error("unexpected end");
      return s.charAt(pos);
    }

    void expect(char c) {
      if (peek() != c) throw error("expected '" + c + "'");
      pos++;
    }

    Object value() {
      char c = peek();
      switch (c) {
        case '{':
          return object();
        case '[':
          return array();
        case '"':
          return string();
        case 't':
          return literal("true", Boolean.TRUE);
        case 'f':
          return literal("false", Boolean.FALSE);
        case 'n':
          return literal("null", null);
        default:
          return number();
      }
    }

    Object literal(String word, Object v) {
      if (!s.startsWith(word, pos)) throw error("expected " + word);
      pos += word.length();
      return v;
    }

    Map<String, Object> object() {
      expect('{');
      var m = new LinkedHashMap<String, Object>();
      if (peek() == '}') {
        pos++;
        return m;
      }
      while (true) {
        if (peek() != '"') throw error("expected object key");
        String k = string();
        expect(':');
        m.put(k, value());
        char c = peek();
        pos++;
        if (c == '}') return m;
        if (c != ',') throw error("expected ',' or '}'");
      }
    }

    List<Object> array() {
      expect('[');
      var l = new ArrayList<Object>();
      if (peek() == ']') {
        pos++;
        return l;
      }
      while (true) {
        l.add(value());
        char c = peek();
        pos++;
        if (c == ']') return l;
        if (c != ',') throw error("expected ',' or ']'");
      }
    }

    String string() {
      expect('"');
      var sb = new StringBuilder();
      while (true) {
        if (pos >= s.length()) throw error("unterminated string");
        char c = s.charAt(pos++);
        if (c == '"') return sb.toString();
        if (c != '\\') {
          sb.append(c);
          continue;
        }
        if (pos >= s.length()) throw error("unterminated escape");
        char e = s.charAt(pos++);
        switch (e) {
          case '"', '\\', '/' -> sb.append(e);
          case 'n' -> sb.append('\n');
          case 'r' -> sb.append('\r');
          case 't' -> sb.append('\t');
          case 'b' -> sb.append('\b');
          case 'f' -> sb.append('\f');
          case 'u' -> {
            if (pos + 4 > s.length()) throw error("bad unicode escape");
            sb.append((char) Integer.parseInt(s.substring(pos, pos + 4), 16));
            pos += 4;
          }
          default -> throw error("bad escape");
        }
      }
    }

    Double number() {
      int begin = pos;
      while (pos < s.length() && "+-0123456789.eE".indexOf(s.charAt(pos)) >= 0) pos++;
      if (begin == pos) throw error("unexpected character");
      try {
        return Double.parseDouble(s.substring(begin, pos));
      } catch (NumberFormatException e) {
        throw error("bad number");
      }
    }
  }
}
