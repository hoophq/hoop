(ns webapp.webclient.mssql-database-test
  (:require
   [cljs.test :refer-macros [deftest is]]
   [webapp.webclient.mssql-database :as mssql-database]))

(def ^:private user-script "SELECT * FROM dbo.t")

(def ^:private terminal-script (mssql-database/with-database "jd-pix" user-script))

(deftest terminal-script-drops-the-database-context-line
  (is (= "id\n--\n3\n"
         (mssql-database/strip-output "Changed database context to 'jd-pix'.\nid\n--\n3\n"
                                      terminal-script))))

(deftest terminal-script-drops-the-localized-database-context-line
  (is (= "id\n--\n3\n"
         (mssql-database/strip-output "Contexto do banco de dados alterado para 'jd-pix'.\nid\n--\n3\n"
                                      terminal-script))))

(deftest runbook-output-keeps-the-error
  (let [output "Incorrect syntax near '-'.\n\nfailed executing command, reason=exit status 1"]
    (is (= output (mssql-database/strip-output output "SELECT * FROM jd-pix.dbo.t")))))

(deftest runbook-output-keeps-the-header
  (is (= "id\n--\n3\n\n(1 row affected)\n"
         (mssql-database/strip-output "id\n--\n3\n\n(1 row affected)\n"
                                      "financial/select.runbook.sql"))))

(deftest missing-script-keeps-the-output
  (is (= "id\n--\n3\n" (mssql-database/strip-output "id\n--\n3\n" nil))))

(deftest missing-output-stays-missing
  (is (nil? (mssql-database/strip-output nil terminal-script))))

(deftest terminal-script-hides-its-prefix
  (is (= user-script (mssql-database/strip-script terminal-script)))
  (is (= user-script (mssql-database/strip-script user-script))))
