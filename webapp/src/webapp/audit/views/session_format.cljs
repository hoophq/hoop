(ns webapp.audit.views.session-format)

;; Subtypes the gateway serves through the hoop SSH proxy.
(def ^:private ssh-subtypes #{"ssh" "ssh-local" "git" "github"})

(defn recording-format [session]
  (or (:recording_format session)
      ;; Before the gateway persisted the protocol used at session start, the
      ;; viewer was selected by the resource type. Keep that choice for old
      ;; recordings: a subtype may have resolved to a different protocol then.
      ;; The ssh viewer replays a stream that is not SSH frames as a PTY.
      (cond
        (and (= "custom" (:type session))
             (= "rdp" (:connection_subtype session))) "rdp"
        (and (= "application" (:type session))
             (contains? ssh-subtypes (:connection_subtype session))) "ssh"
        (contains? #{"command-line" "application" "custom"} (:type session)) "pty"
        :else "raw")))
