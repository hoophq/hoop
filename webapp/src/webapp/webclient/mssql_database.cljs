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

;; USE prints "Changed database context to '<db>'." (localized) as the first line.
(defn strip-output [output script]
  (if (and output (with-database? script))
    (if-let [idx (cs/index-of output "\n")]
      (subs output (inc idx))
      output)
    output))
