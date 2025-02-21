(ns webapp.webclient.components.language-select
  (:require
   [re-frame.core :as rf]
   [webapp.components.forms :as forms]))

(def languages-options
  [{:text "Shell" :value "command-line"}
   {:text "MySQL" :value "mysql"}
   {:text "Postgres" :value "postgres"}
   {:text "SQL Server" :value "mssql"}
   {:text "MongoDB" :value "mongodb"}
   {:text "JavaScript" :value "nodejs"}
   {:text "Python" :value "python"}
   {:text "Ruby" :value "ruby-on-rails"}
   {:text "Clojure" :value "clojure"}])

(defn main []
  (fn []
    (let [language-info @(rf/subscribe [:editor-plugin/language])]
      [forms/select
       {:on-change #(if (= % (:default language-info))
                      (rf/dispatch [:editor-plugin/clear-language])
                      (rf/dispatch [:editor-plugin/set-language %]))
        :size "1"
        :not-margin-bottom? true
        :variant "ghost"
        :selected (or (:selected language-info)
                      (:default language-info))
        :options languages-options}])))
