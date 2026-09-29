(ns webapp.audit.views.session-format)

(defn recording-format [session]
  (or (:recording_format session)
      ;; Before the gateway persisted the protocol used at session start, the
      ;; viewer was selected by the resource type. Keep that choice for old
      ;; recordings: a subtype may have resolved to a different protocol then.
      (cond
        (and (= "custom" (:type session))
             (= "rdp" (:connection_subtype session))) "rdp"
        (contains? #{"command-line" "application" "custom"} (:type session)) "pty"
        :else "raw")))
