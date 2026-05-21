(ns webapp.audit.views.pg-wire
  "ClojureScript port of the parts of `common/pgtypes/packet.go` we need to
  render a Postgres machine session in real time. Specifically, we decode the
  client side of the PG wire protocol so we can show the SQL inside `Parse`
  ('P') and simple `Query` ('Q') frames as they stream in.

  An SSE event from `/sessions/<id>/stream` carries one TCP chunk in `payload`
  (base64). A chunk can contain one or more concatenated PG frames, each laid
  out as: type (1 byte) + length (uint32 big-endian, includes itself) + body.")

(def ^:private known-type-names
  {"P" "Parse"
   "Q" "Query"
   "B" "Bind"
   "D" "Describe"
   "E" "Execute"
   "S" "Sync"
   "C" "Close"
   "H" "Flush"
   "X" "Terminate"
   "F" "FunctionCall"
   "p" "Password"})

(defn type-name [t]
  (get known-type-names t t))

(defn- decode-bytes [b64]
  (let [binary (js/atob b64)
        len (.-length binary)
        bytes (js/Uint8Array. len)]
    (dotimes [i len]
      (aset bytes i (.charCodeAt binary i)))
    bytes))

(defn- read-uint32-be [bytes offset]
  ;; bit-shift-left in CLJS is fine for 24 bits; mask to be safe
  (bit-or
   (bit-shift-left (bit-and (aget bytes offset) 0xff) 24)
   (bit-shift-left (bit-and (aget bytes (+ offset 1)) 0xff) 16)
   (bit-shift-left (bit-and (aget bytes (+ offset 2)) 0xff) 8)
   (bit-and (aget bytes (+ offset 3)) 0xff)))

(defn- bytes->utf8 [bytes start end]
  (when (<= start end)
    (let [slice (.subarray bytes start end)
          decoder (js/TextDecoder. "utf-8" #js {:fatal false})]
      (.decode decoder slice))))

(defn- find-null [bytes start end]
  (loop [i start]
    (cond
      (>= i end) -1
      (zero? (aget bytes i)) i
      :else (recur (inc i)))))

(defn- parse-frame
  "Try to parse a single PG frame from `bytes` starting at `offset`.
  Returns a map with :type, :sql (when applicable) and :next-offset, or nil
  when the frame is truncated."
  [bytes offset]
  (let [total (.-length bytes)]
    (when (>= (- total offset) 5)
      (let [type-byte (aget bytes offset)
            type-char (js/String.fromCharCode type-byte)
            length (read-uint32-be bytes (inc offset))
            ;; total bytes consumed = 1 (type) + `length` (which includes its own 4 bytes)
            frame-end (+ offset 1 length)]
        (when (and (pos? length) (<= frame-end total))
          (let [body-start (+ offset 5)
                body-end frame-end
                sql (cond
                      (= type-char "Q")
                      ;; simple query: SQL terminated by \0
                      (when (> body-end body-start)
                        (bytes->utf8 bytes body-start (dec body-end)))

                      (= type-char "P")
                      ;; Parse: stmt-name\0 SQL\0 + param oids
                      (let [stmt-null (find-null bytes body-start body-end)]
                        (when-not (neg? stmt-null)
                          (let [sql-start (inc stmt-null)
                                sql-null (find-null bytes sql-start body-end)]
                            (when-not (neg? sql-null)
                              (bytes->utf8 bytes sql-start sql-null))))))]
            {:type type-char
             :type-name (type-name type-char)
             :sql sql
             :next-offset frame-end}))))))

(defn parse-payload
  "Parse one or more concatenated PG frames out of a base64-encoded chunk.
  Returns a vector of {:type, :type-name, :sql} maps. SQL is non-nil only for
  Parse and Query frames. Truncated/garbage tails are silently dropped — the
  client may split a logical message across TCP segments and we'd rather skip
  than crash the view."
  [b64]
  (try
    (let [bytes (decode-bytes b64)
          total (.-length bytes)]
      (loop [offset 0
             out []]
        (if (>= offset total)
          out
          (if-let [frame (parse-frame bytes offset)]
            (recur (:next-offset frame)
                   (conj out (select-keys frame [:type :type-name :sql])))
            out))))
    (catch :default _ [])))

(defn query-frame?
  "True when a parsed frame carries an actual SQL string."
  [frame]
  (and frame (string? (:sql frame)) (pos? (count (:sql frame)))))
