(ns webapp.webclient.mssql-database-test
  (:require
   [cljs.test :refer-macros [deftest is]]
   [webapp.webclient.mssql-database :as mssql-database]))

(def ^:private user-script "SELECT * FROM dbo.t")

(deftest drops-the-database-context-line
  (doseq [line ["Changed database context to 'jd-pix'."
                "Changed database context to 'jd-pix'.\r"
                "Contexto do banco de dados alterado para 'jd-pix'."
                "O contexto da base de dados foi alterado para 'jd-pix'."
                "Der Datenbankkontext wurde in \"jd-pix\" geändert."
                "Kontext databáze byl změněn na jd-pix."
                "Zmieniono kontekst bazy danych na „jd-pix”."
                "データベース コンテキストが 'jd-pix' に変更されました。"]]
    (is (= "id\n--\n3\n" (mssql-database/strip-output (str line "\nid\n--\n3\n")))
        line)))

(deftest keeps-the-error
  (let [output "Incorrect syntax near '-'.\n\nfailed executing command, reason=exit status 1"]
    (is (= output (mssql-database/strip-output output)))))

(deftest keeps-the-header
  (let [output "id\n--\n3\n\n(1 row affected)\n"]
    (is (= output (mssql-database/strip-output output)))))

(deftest keeps-an-error-that-arrives-before-the-context-line
  (let [output "Invalid object name 'dbo.nope'.\n\nChanged database context to 'jd-pix'.\n"]
    (is (= output (mssql-database/strip-output output)))))

(deftest keeps-a-line-that-only-starts-like-the-context-line
  (let [output "Changed database context to 'jd-pix'. Extra\nid\n"]
    (is (= output (mssql-database/strip-output output)))))

(deftest missing-output-stays-missing
  (is (nil? (mssql-database/strip-output nil))))

(deftest terminal-script-hides-its-prefix
  (is (= user-script (mssql-database/strip-script
                      (mssql-database/with-database "jd-pix" user-script))))
  (is (= user-script (mssql-database/strip-script user-script))))
