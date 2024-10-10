(ns webapp.components.code-snippet
  (:require ["@heroicons/react/20/solid" :as hero-outline-icon]
            ["clipboard" :as clipboardjs]
            [re-frame.core :as rf]
            [webapp.components.headings :as h]))

;; TODO: move it to another component
(defn copy-clipboard [data-clipboard-target]
  [:div {:class (str "copy-to-clipboard absolute rounded-lg p-x-small "
                     "top-2 right-2 cursor-pointer box-border "
                     "opacity-0 group-hover:opacity-100 transition z-20")
         :data-clipboard-target data-clipboard-target}
   [:> hero-outline-icon/DocumentDuplicateIcon {:class "h-6 w-6 shrink-0 text-white"
                                                :aria-hidden "true"}]])

(defn main
  "config is a map with the following fields:
  :id -> id to differentiate more than one log on the same page.
  :code -> the actual string with the code snippet"
  [config title]
  (let [clipboard (new clipboardjs ".copy-to-clipboard")
        container-id (or (:id config) "code-snippet")]
    (.on clipboard "success" #(rf/dispatch [:show-snackbar {:level :success :text "Text copied to clipboard"}]))
    [:div {:class "overflow-auto"}
     (when title [h/h3 title {:class "mb-regular"}])
     [:section
      {:class (str "relative bg-gray-900 font-mono overflow-auto "
                   " whitespace-pre text-white text-sm rounded-lg"
                   " p-regular group")}
      (when-not (:not-clipboard? config) (copy-clipboard (str "#" container-id)))
      [:div
       {:id container-id
        :class (str (when (:classes config) (:classes config))
                    " overflow-auto whitespace-pre-line "
                    (when-not (:fixed-height? config) " max-h-80"))}
       (:code config)]]]))
