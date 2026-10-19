(ns webapp.webclient.mssql-database
  (:require [clojure.string :as cs]))

(def ^:private prefix-start "SET NOCOUNT ON;\nUSE [")

(defn with-database [database script]
  (str prefix-start database "];\n" script))

(defn- with-database? [script]
  (and (string? script) (cs/starts-with? script prefix-start)))

(defn strip-script [script]
  (if (with-database? script)
    (cs/join "\n" (drop 2 (cs/split script #"\n")))
    script))

;; SQL Server message 5701, printed by USE. A server in another language prints
;; it translated: only the terminal prefix catches that one.
(def ^:private database-context-line #"^Changed database context to '.*'\.\r?$")

(defn strip-output [output script]
  (if-let [idx (and output (cs/index-of output "\n"))]
    (if (or (re-find database-context-line (subs output 0 idx))
            (with-database? script))
      (subs output (inc idx))
      output)
    output))
