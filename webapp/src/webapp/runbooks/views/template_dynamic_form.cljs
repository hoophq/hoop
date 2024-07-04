(ns webapp.runbooks.views.template-dynamic-form
  (:require [webapp.components.forms :as forms]))

(defn dynamic-form
  [type {:keys [label
                on-change
                placeholder
                value
                pattern
                required
                minlength
                maxlength
                min
                max
                step
                helper-text
                options]}]
  [:div
   (case type
     "select" [forms/select (merge
                             {:label label
                              :on-change on-change
                              :selected (or value "")
                              :options (map #(into {} {:value % :text %}) options)
                              :helper-text helper-text}
                             (when (and
                                    (not= required "false")
                                    (or required (nil? required)))
                               {:required true}))]
     [forms/input (merge
                   {:label label
                    :placeholder (or placeholder (str "Define a value for " label))
                    :value value
                    :type type
                    :pattern pattern
                    :on-change on-change
                    :minLength minlength
                    :maxLength maxlength
                    :min min
                    :max max
                    :step step
                    :helper-text helper-text}
                   (when (and
                          (not= required "false")
                          (or required (nil? required)))
                     {:required true}))])])
