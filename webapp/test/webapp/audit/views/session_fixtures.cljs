(ns webapp.audit.views.session-fixtures
  "Writes the session test vectors as JSON fixtures for the webapp_v2 tests.
  Each case holds its input and the output of the ClojureScript code, which
  must also pass the test's own check. Run: npm run fixtures:sessions"
  (:require
   ["fs" :as fs]
   ["path" :as path]
   [webapp.audit.views.session-vectors :as v]))

(def ^:private source "webapp/test/webapp/audit/views/session_vectors.cljs")
(def ^:private generator "npm --prefix webapp run fixtures:sessions")

(defn- ssh-json [result]
  (when result
    {"terminal" (:terminal? result)
     "data" (:data? result)
     "width" (first (:size result))
     "height" (second (:size result))
     "texts" (:texts result)}))

(def ^:private fixtures
  [{:file "session_format.json"
    :function "webapp.audit.views.session-format/recording-format"
    :cases v/format-cases
    :run v/run-format
    :passes? =
    :json (fn [c output] {"name" (:name c)
                          "session" (:session c)
                          "expected" output})}
   {:file "terminal_decoder.json"
    :function "webapp.audit.views.terminal-decoder/decode-events"
    :cases v/terminal-cases
    :run v/run-terminal
    :passes? =
    :json (fn [c output] {"name" (:name c)
                          "events" (:events c)
                          "finalize" (:finalize? c)
                          "expected" output})}
   {:file "ssh_decoder.json"
    :function "webapp.audit.views.ssh-decoder/terminal-recording"
    :cases v/ssh-cases
    :run v/run-ssh
    :passes? v/ssh-matches?
    :json (fn [c output] {"name" (:name c)
                          "events" (:events c)
                          "expected" (ssh-json output)})}
   {:file "session_live_tail.json"
    :function "webapp.audit.views.session-live-tail/live-terminal-events"
    :cases v/live-tail-cases
    :run v/run-live-tail
    :passes? =
    :json (fn [c output] {"name" (:name c)
                          "recording_format" (:recording-format c)
                          "events" (:events c)
                          "finalize" (:finalize? c)
                          "expected" output})}])

(defn- fixture-cases
  "The JSON cases of one fixture, or throws on a case whose output fails the
  test's check: a fixture never records a behavior the tests reject."
  [{:keys [file cases run passes? json]}]
  (mapv (fn [c]
          (let [output (run c)]
            (when-not (passes? (:expected c) output)
              (throw (ex-info (str file ": case \"" (:name c) "\" fails its test")
                              {:expected (:expected c) :output output})))
            (json c output)))
        cases))

(defn main [& [out-dir]]
  (when-not out-dir
    (throw (ex-info "usage: node target/session-fixtures.js <out-dir>" {})))
  (fs/mkdirSync out-dir #js {:recursive true})
  (doseq [{:keys [file function] :as fixture} fixtures]
    (let [doc {"source" source
               "generator" generator
               "function" function
               "cases" (fixture-cases fixture)}
          target (path/join out-dir file)]
      (fs/writeFileSync target (str (js/JSON.stringify (clj->js doc) nil 2) "\n"))
      (println "wrote" target))))
